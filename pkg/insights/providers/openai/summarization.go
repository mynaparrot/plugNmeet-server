package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	sdk "github.com/openai/openai-go/v3"
	"github.com/sirupsen/logrus"
)

const (
	batchJobRedisKeyPrefix = "pnm:insights:openai_batch:"
	batchJobRedisExpiry    = time.Hour * 24

	// OpenAI audio upload limit.
	maxOpenAIPayloadBytes = 25 * 1024 * 1024
	// Use a safer target when generating chunks to stay comfortably under OpenAI's 25 MB upload limit.
	safeChunkSizeBytes = 20 * 1024 * 1024
)

// StartBatchSummarizeAudioFile starts a simulated asynchronous summarization job.
func (p *OpenAIProvider) StartBatchSummarizeAudioFile(ctx context.Context, filePath, summarizeModel, userPrompt string) (jobId string, fileName string, err error) {
	jobId = uuid.NewString()

	log := p.logger.WithFields(logrus.Fields{
		"jobId":    jobId,
		"filePath": filePath,
		"model":    summarizeModel,
	})

	if p.redis == nil {
		return "", "", fmt.Errorf("redis service is not available for batch processing")
	}

	log.Infoln("Starting openai batch summarization job")

	p.updateJobStatus(jobId, insights.BatchJobStatusRunning, "", "", 0, 0, 0)

	go p.runBatchJob(jobId, filePath, summarizeModel, userPrompt, log)

	return jobId, filePath, nil
}

func (p *OpenAIProvider) runBatchJob(jobId, filePath, summarizeModel, userPrompt string, log *logrus.Entry) {
	ctx := context.Background()

	transcript, totalTranscribeToken, err := p.transcribeFile(ctx, filePath)
	if err != nil {
		log.WithError(err).Error("failed to transcribe audio file")
		p.updateJobStatus(jobId, insights.BatchJobStatusFailed, "", err.Error(), 0, 0, 0)
		return
	}

	if userPrompt == "" {
		userPrompt = "Summarize this meeting conversation. Identify all key decisions and create a list of action items."
	}

	summarizationPrompt := fmt.Sprintf(
		"You are a professional meeting summarization assistant. Your response must be a single, valid JSON object with one key: 'summary'. The value of the 'summary' key should be a clean, well-formatted HTML string using basic tags like <h3> for section headings, <ul>, <li>, <p>, and <strong>. The user's instruction for the summary is: \n\n---\n\n%s",
		userPrompt,
	)

	messages := []sdk.ChatCompletionMessageParamUnion{
		sdk.SystemMessage(summarizationPrompt),
		sdk.UserMessage(transcript),
	}

	if summarizeModel == "" {
		summarizeModel = sdk.ChatModelGPT5_4Mini
	}

	resp, err := p.openAiClient.Chat.Completions.New(ctx, sdk.ChatCompletionNewParams{
		Model:    summarizeModel,
		Messages: messages,
	})
	if err != nil {
		p.updateJobStatus(jobId, insights.BatchJobStatusFailed, "", err.Error(), 0, 0, 0)
		log.WithError(err).Error("failed to execute summarize request")
		return
	}

	if len(resp.Choices) == 0 {
		errMsg := "no summary content found in response"
		p.updateJobStatus(jobId, insights.BatchJobStatusFailed, "", errMsg, 0, 0, 0)
		log.Error(errMsg)
		return
	}

	summary := resp.Choices[0].Message.Content
	promptTokens := resp.Usage.PromptTokens
	completionTokens := resp.Usage.CompletionTokens

	var summaryData struct {
		Summary string `json:"summary"`
	}

	cleanedSummary := cleanJSONResponse(summary)

	if err := json.Unmarshal([]byte(cleanedSummary), &summaryData); err == nil && strings.TrimSpace(summaryData.Summary) != "" {
		summary = summaryData.Summary
	} else if err != nil {
		log.WithError(err).Warn("failed to unmarshal summary JSON, using raw text as fallback")
	}

	p.updateJobStatus(
		jobId,
		insights.BatchJobStatusCompleted,
		summary,
		"",
		promptTokens,
		completionTokens,
		promptTokens+completionTokens+totalTranscribeToken,
	)

	log.Infoln("Batch job completed successfully")
}

// transcribeFile sends small files directly and chunks files larger than 25 MB.
func (p *OpenAIProvider) transcribeFile(ctx context.Context, filePath string) (string, int64, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to stat file: %w", err)
	}

	model := p.service.GetOptionsString("transcription_model", sdk.AudioModelGPT4oTranscribe)

	if fileInfo.Size() <= safeChunkSizeBytes {
		return p.executeSingleTranscription(ctx, filePath, model, false)
	}

	return p.transcribeLargeWavInChunks(ctx, filePath, model)
}

// executeSingleTranscription sends one audio file to OpenAI.
func (p *OpenAIProvider) executeSingleTranscription(ctx context.Context, filePath, model string, allowEmpty bool) (string, int64, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to stat transcription file: %w", err)
	}

	if fileInfo.Size() > maxOpenAIPayloadBytes {
		return "", 0, fmt.Errorf(
			"transcription file %s is too large: %d bytes exceeds %d bytes",
			filepath.Base(filePath),
			fileInfo.Size(),
			maxOpenAIPayloadBytes,
		)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	transcription, err := p.openAiClient.Audio.Transcriptions.New(ctx, sdk.AudioTranscriptionNewParams{
		Model:          model,
		File:           file,
		ResponseFormat: sdk.AudioResponseFormatJSON,
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to execute transcription request: %w", err)
	}

	text := strings.TrimSpace(transcription.Text)
	if text == "" && !allowEmpty {
		return "", transcription.Usage.TotalTokens, fmt.Errorf("empty transcription text returned")
	}

	return text, transcription.Usage.TotalTokens, nil
}

// transcribeLargeWavInChunks splits files larger than 25 MB into smaller WAV chunks and transcribes each chunk.
// Chunks are re-encoded to a fixed 16 kHz mono PCM WAV format so the output
// bitrate is deterministic and chunk sizes are predictable regardless of the
// source codec or bitrate.
func (p *OpenAIProvider) transcribeLargeWavInChunks(ctx context.Context, filePath, model string) (string, int64, error) {
	const (
		targetSampleRate     = 16000
		targetChannels       = 1
		targetBytesPerSecond = targetSampleRate * 2 * targetChannels // 16-bit samples
	)

	segmentTimeSeconds := safeChunkSizeBytes / targetBytesPerSecond

	tmpDir, err := os.MkdirTemp("", "pnm-wav-split-*")
	if err != nil {
		return "", 0, fmt.Errorf("failed to create temp directory for chunks: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outputPattern := filepath.Join(tmpDir, "chunk_%03d.wav")

	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-y", "-i", filePath,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-f", "segment",
		"-segment_time", fmt.Sprintf("%d", segmentTimeSeconds), "-reset_timestamps", "1",
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
		outputPattern,
	)

	ffmpegOut, err := cmd.CombinedOutput()
	if err != nil {
		return "", 0, fmt.Errorf(
			"ffmpeg split failed: %w: %s",
			err,
			strings.TrimSpace(string(ffmpegOut)),
		)
	}

	chunkPaths, err := listChunkPaths(tmpDir)
	if err != nil {
		return "", 0, err
	}

	if len(chunkPaths) == 0 {
		return "", 0, fmt.Errorf("no usable wav chunks found after ffmpeg split")
	}

	var totalTokens int64
	var transcripts []string

	for _, chunkPath := range chunkPaths {
		chunkInfo, err := os.Stat(chunkPath)
		if err != nil {
			return "", totalTokens, fmt.Errorf("failed to stat chunk %s: %w", filepath.Base(chunkPath), err)
		}

		if chunkInfo.Size() == 0 {
			continue
		}

		if chunkInfo.Size() > maxOpenAIPayloadBytes {
			return "", totalTokens, fmt.Errorf(
				"chunk %s is still too large: %d bytes exceeds %d bytes",
				filepath.Base(chunkPath),
				chunkInfo.Size(),
				maxOpenAIPayloadBytes,
			)
		}

		text, tokens, err := p.executeSingleTranscription(ctx, chunkPath, model, true)
		if err != nil {
			return "", totalTokens, fmt.Errorf("failed to transcribe chunk %s: %w", filepath.Base(chunkPath), err)
		}

		totalTokens += tokens

		if cleanedText := strings.TrimSpace(text); cleanedText != "" {
			transcripts = append(transcripts, cleanedText)
		}
	}

	if len(transcripts) == 0 {
		return "", totalTokens, fmt.Errorf("all chunks produced empty transcription")
	}

	return strings.Join(transcripts, " "), totalTokens, nil
}

func listChunkPaths(tmpDir string) ([]string, error) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk directory: %w", err)
	}

	var paths []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasPrefix(name, "chunk_") && strings.HasSuffix(name, ".wav") {
			paths = append(paths, filepath.Join(tmpDir, name))
		}
	}

	sort.Strings(paths)

	return paths, nil
}

// cleanJSONResponse strips markdown code fences if the model wraps its JSON response.
func cleanJSONResponse(raw string) string {
	cleaned := strings.TrimSpace(raw)

	if strings.HasPrefix(cleaned, "```json") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimSuffix(cleaned, "```")
	} else if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
	}

	return strings.TrimSpace(cleaned)
}

// CheckBatchJobStatus checks the status of a previously started batch job from Redis.
func (p *OpenAIProvider) CheckBatchJobStatus(ctx context.Context, jobId string) (*insights.BatchJobResponse, error) {
	if p.redis == nil {
		return nil, fmt.Errorf("redis service is not available for batch processing")
	}

	key := batchJobRedisKeyPrefix + jobId

	data, err := p.redis.Get(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get batch job status from redis: %w", err)
	}

	var res insights.BatchJobResponse
	if err := json.Unmarshal([]byte(data), &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal batch job status: %w", err)
	}

	return &res, nil
}

// updateJobStatus stores the job result in Redis.
func (p *OpenAIProvider) updateJobStatus(jobId string, status insights.BatchJobStatus, summary, errorMsg string, promptTokens, completionTokens, totalTokens int64) {
	if p.redis == nil {
		p.logger.Error("redis service is not available, cannot update job status")
		return
	}

	key := batchJobRedisKeyPrefix + jobId

	res := insights.BatchJobResponse{
		Status:           status,
		Error:            errorMsg,
		Summary:          summary,
		PromptTokens:     uint32(promptTokens),
		CompletionTokens: uint32(completionTokens),
		TotalTokens:      uint32(totalTokens),
	}

	data, err := json.Marshal(res)
	if err != nil {
		p.logger.WithError(err).Error("failed to marshal job status")
		return
	}

	if err := p.redis.Set(context.Background(), key, data, batchJobRedisExpiry).Err(); err != nil {
		p.logger.WithError(err).Error("failed to set batch job status in redis")
	}
}
