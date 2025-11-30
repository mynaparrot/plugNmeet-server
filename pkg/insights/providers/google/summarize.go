package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

// StartBatchSummarizeAudioFile implements the provider interface using the correct Gemini batch API.
func (p *GoogleProvider) StartBatchSummarizeAudioFile(ctx context.Context, filePath, summarizeModel, userPrompt string) (string, string, error) {
	log := p.logger.WithFields(logrus.Fields{
		"filePath": filePath,
		"model":    summarizeModel,
	})
	if userPrompt == "" {
		userPrompt = "Summarize this meeting conversation. Identify all key decisions and create a list of action items."
	}
	log.Infof("using summarizationPrompt: '%s'", userPrompt)

	summarizationPrompt := fmt.Sprintf("You are a professional meeting summarization assistant. Your response must be a single, valid JSON object with one key: 'summary'. The value of the 'summary' key should be the result of the following user instruction:\n\n---\n\n%s", userPrompt)

	// 1. Upload the file to Google Cloud Storage.
	f, err := p.client.Files.UploadFromPath(ctx, filePath, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to upload file to google: %w", err)
	}
	log = log.WithFields(logrus.Fields{
		"url":            f.URI,
		"mime-type":      f.MIMEType,
		"expirationTime": f.ExpirationTime.Format(time.RFC3339),
	})
	log.Infoln("successfully uploaded file")

	contents := []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromURI(f.URI, f.MIMEType),
		}, genai.RoleUser),
	}
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				genai.NewPartFromText(summarizationPrompt),
			},
		},
	}

	// Construct the inlined request for the batch job.
	inlineRequests := []*genai.InlinedRequest{
		{
			Contents: contents,
			Config:   config,
		},
	}

	// Create the batch job using the correct client.Batches.Create method.
	batchJob, err := p.client.Batches.Create(
		ctx,
		summarizeModel,
		&genai.BatchJobSource{
			InlinedRequests: inlineRequests,
		},
		nil,
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to create batch job: %w", err)
	}
	log.WithField("job-id", batchJob.Name).Infoln("successfully created batch job")

	return batchJob.Name, f.Name, nil
}

// CheckBatchJobStatus implements the provider interface.
func (p *GoogleProvider) CheckBatchJobStatus(ctx context.Context, jobId string) (*insights.BatchJobResponse, error) {
	job, err := p.client.Batches.Get(ctx, jobId, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get batch job status: %w", err)
	}

	res := &insights.BatchJobResponse{
		Status: insights.BatchJobStatusRunning,
	}

	if job.State == genai.JobStateSucceeded {
		inlinedResponses := job.Dest.InlinedResponses
		// from the batch API, we'll always get the result in the first response
		if len(inlinedResponses) > 0 {
			inlinedRes := inlinedResponses[0]
			if inlinedRes.Error != nil {
				res.Error = fmt.Sprintf("%v: %s", inlinedRes.Error.Code, inlinedRes.Error.Message)
				res.Status = insights.BatchJobStatusFailed
				return res, nil
			}

			if len(inlinedRes.Response.Candidates) > 0 {
				candidate := inlinedRes.Response.Candidates[0]
				if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
					part := candidate.Content.Parts[0]
					jsonString := strings.TrimSpace(part.Text)
					jsonString = strings.TrimPrefix(jsonString, "```json")
					jsonString = strings.TrimSuffix(jsonString, "```")

					var summaryData struct {
						Summary string `json:"summary"`
					}

					if err := json.Unmarshal([]byte(jsonString), &summaryData); err == nil {
						res.Summary = summaryData.Summary
					} else {
						// Fallback if the model didn't return perfect JSON
						p.logger.WithError(err).Warn("failed to unmarshal summary JSON, using raw text as fallback")
						res.Summary = part.Text
					}
					res.Status = insights.BatchJobStatusCompleted
				}
			}
			if inlinedRes.Response.UsageMetadata != nil {
				res.PromptTokens = uint32(inlinedRes.Response.UsageMetadata.PromptTokenCount)
				res.CompletionTokens = uint32(inlinedRes.Response.UsageMetadata.CandidatesTokenCount)
				res.TotalTokens = uint32(inlinedRes.Response.UsageMetadata.TotalTokenCount)
			}
		}
	} else if job.State == genai.JobStateFailed {
		res.Status = insights.BatchJobStatusFailed
		if job.Error != nil {
			res.Error = job.Error.Message
		} else {
			res.Error = "job failed with an unknown error"
		}
	}

	return res, nil
}

// DeleteUploadedFile implements the provider interface.
func (p *GoogleProvider) DeleteUploadedFile(ctx context.Context, fileName string) error {
	_, err := p.client.Files.Delete(ctx, fileName, nil)
	if err != nil {
		return err
	}
	return nil
}
