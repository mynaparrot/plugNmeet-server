package models

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
)

// CheckInsightsPendingSummarizeJobs checks the status of pending summarization jobs.
func (m *JanitorModel) CheckInsightsPendingSummarizeJobs() {
	jobs, err := m.app.RDS.HGetAll(m.ctx, insights.PendingSummarizeJobRedisKey).Result()
	if err != nil {
		m.logger.WithError(err).Error("failed to get pending summarization jobs from Redis")
		return
	}

	if len(jobs) == 0 {
		return
	}

	log := m.logger.WithField("task", "summarize_job_checker")
	log.Infof("checking status for %d pending summarization jobs", len(jobs))

	// We only need one provider instance for this service.
	targetAccount, serviceConfig, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeMeetingSummarizing)
	if err != nil {
		log.WithError(err).Error("failed to get provider account for summarization service")
		return
	}
	provider, err := insightsservice.NewProvider(m.ctx, serviceConfig.Provider, targetAccount, serviceConfig, m.logger)
	if err != nil {
		log.WithError(err).Error("failed to create provider for summarization service")
		return
	}

	for id, jobData := range jobs {
		var payload insights.SummarizePendingJobPayload
		err := json.Unmarshal([]byte(jobData), &payload)
		if err != nil {
			log.WithError(err).Errorf("failed to unmarshal pending job payload for job %s", id)
			continue
		}

		res, err := provider.CheckBatchJobStatus(m.ctx, payload.JobId)
		if err != nil {
			log.WithError(err).Errorf("failed to check res for job %s", payload.JobId)
			continue
		}

		cleanup := func() {
			// Delete from provider storage
			if err := provider.DeleteUploadedFile(m.ctx, payload.FileName); err != nil {
				log.WithError(err).Errorf("failed to delete provider file %s for job %s", payload.FileName, payload.JobId)
			}
			// Delete from our local storage
			if err := os.Remove(payload.OriginalFilePath); err != nil {
				log.WithError(err).Errorf("failed to delete local file %s for job %s", payload.OriginalFilePath, payload.JobId)
			}
			// Delete from Redis
			if err := m.app.RDS.HDel(m.ctx, insights.PendingSummarizeJobRedisKey, id).Err(); err != nil {
				log.WithError(err).Errorf("failed to delete job %s from Redis", id)
			}
		}

		switch res.Status {
		case insights.BatchJobStatusCompleted:
			log.Infof("job %s completed successfully", payload.JobId)
			// TODO: Save the summary (res.Summary) to the database.
			fmt.Println(fmt.Sprintf("%+v", payload))
			log.Infof("Summary for job %s: %s", payload.JobId, res.Summary)
			cleanup()

		case insights.BatchJobStatusFailed:
			log.Errorf("job %s failed: %s", payload.JobId, res.Error)
			cleanup()

		case insights.BatchJobStatusRunning:
			// Still running, do nothing.
			log.Infof("job %s is still running", payload.JobId)

		default:
			log.Warnf("unknown res '%s' for job %s", res.Status, payload.JobId)
		}
	}
}
