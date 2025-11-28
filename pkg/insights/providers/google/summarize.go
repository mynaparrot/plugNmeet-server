package google

import (
	"context"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"google.golang.org/genai"
)

// StartBatchSummarizeAudioFile implements the provider interface using the correct Gemini batch API.
func (p *GoogleProvider) StartBatchSummarizeAudioFile(ctx context.Context, filePath, summarizeModel, summarizationPrompt string) (string, string, error) {
	// 1. Upload the file to Google Cloud Storage.
	f, err := p.client.Files.UploadFromPath(ctx, filePath, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to upload file to google: %w", err)
	}
	if summarizationPrompt == "" {
		summarizationPrompt = "Summarize this meeting conversation. Identify all key decisions and create a list of action items with assigned owners."
	}

	parts := []*genai.Part{
		genai.NewPartFromURI(f.URI, f.MIMEType),
		genai.NewPartFromText("\n\n"),
		genai.NewPartFromText(summarizationPrompt),
	}

	// 2. Construct the inlined request for the batch job.
	inlineRequests := []*genai.InlinedRequest{
		{
			Contents: []*genai.Content{
				{
					Parts: parts,
					Role:  "user",
				},
			},
		},
	}

	// 3. Create the batch job using the correct client.Batches.Create method.
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
		res.Status = "COMPLETED"
		inlinedResponses := job.Dest.InlinedResponses
		// Assuming the first response is the one we want.
		if len(inlinedResponses) > 0 {
			inlinedRes := inlinedResponses[0]
			candidate := inlinedRes.Response.Candidates[0]
			if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
				res.Summary = candidate.Content.Parts[0].Text
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
