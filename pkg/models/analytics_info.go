package models

import (
	"errors"
	"path/filepath"
	"sort"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *AnalyticsModel) FetchAnalytics(r *plugnmeet.FetchAnalyticsReq) (*plugnmeet.FetchAnalyticsResult, error) {
	if r.Limit <= 0 {
		r.Limit = 20
	} else if r.Limit > 100 {
		r.Limit = 100
	}
	if r.OrderBy == "" {
		r.OrderBy = "DESC"
	}

	// 1. Fetch from the new artifacts system
	artifactType := plugnmeet.RoomArtifactType_MEETING_ANALYTICS
	artifacts, err := m.artifactModel.FetchArtifacts(&plugnmeet.FetchArtifactsReq{
		RoomIds: r.RoomIds,
		Type:    &artifactType,
		Limit:   uint64(r.Limit),
		From:    uint64(r.From),
		OrderBy: r.OrderBy,
	})
	if err != nil {
		return nil, err
	}

	var analytics []*plugnmeet.AnalyticsInfo
	for _, v := range artifacts.ArtifactsList {
		if v.Metadata == nil || v.Metadata.FileInfo == nil {
			continue
		}

		created, _ := time.Parse(time.RFC3339, v.Created)
		analytic := &plugnmeet.AnalyticsInfo{
			RoomId:       v.RoomId,
			FileId:       v.ArtifactId,
			FileSize:     float64(v.Metadata.FileInfo.FileSize),
			FileName:     filepath.Base(v.Metadata.FileInfo.FilePath),
			CreationTime: created.Unix(),
		}
		analytics = append(analytics, analytic)
	}

	// 2. Fetch from the old analytics table
	oldData, totalOld, err := m.ds.GetAnalytics(r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}

	for _, v := range oldData {
		// Avoid duplicates - if it was already fetched from artifacts, skip it.
		isDuplicate := false
		for _, existing := range analytics {
			if existing.FileId == v.FileID {
				isDuplicate = true
				break
			}
		}
		if isDuplicate {
			continue
		}

		analytic := &plugnmeet.AnalyticsInfo{
			RoomId:       v.RoomID,
			FileId:       v.FileID,
			FileSize:     v.FileSize,
			FileName:     v.FileName,
			CreationTime: v.CreationTime,
		}
		analytics = append(analytics, analytic)
	}

	// 3. Sort the combined list
	sort.SliceStable(analytics, func(i, j int) bool {
		if r.OrderBy == "DESC" {
			return analytics[i].CreationTime > analytics[j].CreationTime
		}
		return analytics[i].CreationTime < analytics[j].CreationTime
	})

	// 4. Apply pagination to the merged list
	total := int64(len(analytics))
	start := int64(r.From)
	end := start + int64(r.Limit)
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginatedAnalytics := analytics[start:end]

	result := &plugnmeet.FetchAnalyticsResult{
		TotalAnalytics: totalOld + artifacts.TotalArtifacts,
		From:           r.From,
		Limit:          r.Limit,
		OrderBy:        r.OrderBy,
		AnalyticsList:  paginatedAnalytics,
	}

	return result, nil
}

func (m *AnalyticsModel) fetchAnalytic(fileId string) (*plugnmeet.AnalyticsInfo, error) {
	v, err := m.ds.GetAnalyticByFileId(fileId)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, errors.New("no info found")
	}
	analytic := &plugnmeet.AnalyticsInfo{
		RoomId:       v.RoomID,
		FileId:       v.FileID,
		FileSize:     v.FileSize,
		FileName:     v.FileName,
		CreationTime: v.CreationTime,
	}

	return analytic, nil
}
