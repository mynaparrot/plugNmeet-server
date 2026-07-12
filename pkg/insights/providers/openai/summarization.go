package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	sdk "github.com/openai/openai-go/v3"
	"github.com/sirupsen/logrus"
)

const (
	batchJobRedisKeyPrefix = "pnm:insights:openai_batch:"
	batchJobRedisExpiry    = time.Hour * 24 // 24 hours
)

// StartBatchSummarizeAudioFile starts a "simulated" asynchronous summarization job.
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
	// Set initial status
	p.updateJobStatus(jobId, insights.BatchJobStatusRunning, "", "", 0, 0, 0)

	go p.runBatchJob(jobId, filePath, summarizeModel, userPrompt, log)

	// For OpenAI, we don't upload to a separate storage, so we can just use the original file path as the "fileName"
	return jobId, filePath, nil
}

func (p *OpenAIProvider) runBatchJob(jobId, filePath, summarizeModel, userPrompt string, log *logrus.Entry) {
	ctx := context.Background()

	// Step 1: Transcribe the audio file
	transcript, totalTranscribeToken, err := p.transcribeFile(ctx, filePath)
	if err != nil {
		log.WithError(err).Error("failed to transcribe audio file")
		p.updateJobStatus(jobId, insights.BatchJobStatusFailed, "", err.Error(), 0, 0, 0)
		return
	}

	// Step 2: Summarize the transcript, using the exact prompt format from the Google provider
	if userPrompt == "" {
		userPrompt = "Summarize this meeting conversation. Identify all key decisions and create a list of action items."
	}
	summarizationPrompt := fmt.Sprintf("You are a professional meeting summarization assistant. Your response must be a single, valid JSON object with one key: 'summary'. The value of the 'summary' key should be a clean, well-formatted HTML string using basic tags like <h3> for section headings, <ul>, <li>, <p>, and <strong>. The user's instruction for the summary is: \n\n---\n\n%s", userPrompt)

	messages := []sdk.ChatCompletionMessageParamUnion{
		sdk.SystemMessage(summarizationPrompt),
		sdk.UserMessage(transcript),
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
		p.updateJobStatus(jobId, insights.BatchJobStatusFailed, "", "no summary content found in response", 0, 0, 0)
		log.Error("no summary content found in response")
		return
	}

	summary := resp.Choices[0].Message.Content
	promptTokens := resp.Usage.PromptTokens
	completionTokens := resp.Usage.CompletionTokens

	// The model is asked to return JSON, but we'll parse it to be safe, just like the Google provider.
	var summaryData struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(summary), &summaryData); err == nil {
		summary = summaryData.Summary
	} else {
		log.WithError(err).Warn("failed to unmarshal summary JSON, using raw text as fallback")
	}

	// Step 3: Store the result in Redis
	p.updateJobStatus(jobId, insights.BatchJobStatusCompleted, summary, "", promptTokens, completionTokens, promptTokens+completionTokens+totalTranscribeToken)
	log.Infoln("Batch job completed successfully")
}

func (p *OpenAIProvider) transcribeFile(ctx context.Context, filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	model := p.service.GetOptionsString("transcription_model", sdk.AudioModelGPT4oTranscribe)

	transcription, err := p.openAiClient.Audio.Transcriptions.New(ctx, sdk.AudioTranscriptionNewParams{
		Model: model,
		File:  file,
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to execute transcription request: %w", err)
	}

	if transcription.Text == "" {
		return "", 0, fmt.Errorf("empty transcription text returned")
	}

	return transcription.Text, transcription.Usage.TotalTokens, nil
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
