// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awss3receiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awss3receiver"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

type s3Reader struct {
	logger *zap.Logger

	listObjectsClient ListObjectsAPI
	getObjectClient   GetObjectAPI
	s3Bucket          string
	s3Prefix          string
	s3Partition       string
	filePrefix        string
	startTime         time.Time
	endTime           time.Time
}

type s3ReaderDataCallback func(context.Context, string, []byte) error

func newS3Reader(ctx context.Context, logger *zap.Logger, cfg *Config) (*s3Reader, error) {
	listObjectsClient, getObjectClient, err := newS3Client(ctx, cfg.S3Downloader)
	if err != nil {
		return nil, err
	}
	startTime, err := parseTime(cfg.StartTime, "starttime")
	if err != nil {
		return nil, err
	}
	endTime, err := parseTime(cfg.EndTime, "endtime")
	if err != nil {
		return nil, err
	}
	if cfg.S3Downloader.S3Partition != S3PartitionHour && cfg.S3Downloader.S3Partition != S3PartitionMinute {
		return nil, errors.New("s3_partition must be either 'hour' or 'minute'")
	}

	return &s3Reader{
		logger:            logger,
		listObjectsClient: listObjectsClient,
		getObjectClient:   getObjectClient,
		s3Bucket:          cfg.S3Downloader.S3Bucket,
		s3Prefix:          cfg.S3Downloader.S3Prefix,
		filePrefix:        cfg.S3Downloader.FilePrefix,
		s3Partition:       cfg.S3Downloader.S3Partition,
		startTime:         startTime,
		endTime:           endTime,
	}, nil
}

func (s3Reader *s3Reader) readAll(ctx context.Context, telemetryType string, dataCallback s3ReaderDataCallback) error {
	var timeStep time.Duration
	if s3Reader.s3Partition == "hour" {
		timeStep = time.Hour
	} else {
		timeStep = time.Minute
	}
	s3Reader.logger.Info("Start reading telemetry")
	for currentTime := s3Reader.startTime; currentTime.Before(s3Reader.endTime); currentTime = currentTime.Add(timeStep) {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := s3Reader.readTelemetryForTime(ctx, currentTime, telemetryType, dataCallback); err != nil {
				return err
			}
		}
	}
	s3Reader.logger.Info("Finished reading telemetry")
	return nil
}

func (s3Reader *s3Reader) readTelemetryForTime(ctx context.Context, t time.Time, telemetryType string, dataCallback s3ReaderDataCallback) error {
	params := &s3.ListObjectsV2Input{
		Bucket: &s3Reader.s3Bucket,
	}
	prefix := s3Reader.getObjectPrefixForTime(t, telemetryType)
	params.Prefix = &prefix
	s3Reader.logger.Debug("Reading telemetry for time", zap.String("prefix", prefix))
	p := s3Reader.listObjectsClient.NewListObjectsV2Paginator(params)

	firstPage := true
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		if firstPage && len(page.Contents) == 0 {
			s3Reader.logger.Info("No telemetry found for time", zap.String("prefix", prefix))
		} else {
			for _, obj := range page.Contents {
				data, err := s3Reader.retrieveObject(ctx, *obj.Key)
				if err != nil {
					return err
				}
				s3Reader.logger.Debug("Retrieved telemetry", zap.String("key", *obj.Key))
				if err := dataCallback(ctx, *obj.Key, data); err != nil {
					return err
				}
			}
		}
		firstPage = false
	}
	return nil
}

func (s3Reader *s3Reader) getObjectPrefixForTime(t time.Time, telemetryType string) string {
	var timeKey string
	switch s3Reader.s3Partition {
	case S3PartitionMinute:
		timeKey = getTimeKeyPartitionMinute(t)
	case S3PartitionHour:
		timeKey = getTimeKeyPartitionHour(t)
	}
	if s3Reader.s3Prefix != "" {
		return fmt.Sprintf("%s/%s/%s%s_", s3Reader.s3Prefix, timeKey, s3Reader.filePrefix, telemetryType)
	}
	return fmt.Sprintf("%s/%s%s_", timeKey, s3Reader.filePrefix, telemetryType)
}

func (s3Reader *s3Reader) retrieveObject(ctx context.Context, key string) ([]byte, error) {
	params := s3.GetObjectInput{
		Bucket: &s3Reader.s3Bucket,
		Key:    &key,
	}
	output, err := s3Reader.getObjectClient.GetObject(ctx, &params)
	if err != nil {
		return nil, err
	}
	defer output.Body.Close()
	contents, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, err
	}
	return contents, nil
}

func getTimeKeyPartitionHour(t time.Time) string {
	year, month, day := t.Date()
	hour := t.Hour()
	return fmt.Sprintf("year=%d/month=%02d/day=%02d/hour=%02d", year, month, day, hour)
}

func getTimeKeyPartitionMinute(t time.Time) string {
	year, month, day := t.Date()
	hour, minute, _ := t.Clock()
	return fmt.Sprintf("year=%d/month=%02d/day=%02d/hour=%02d/minute=%02d", year, month, day, hour, minute)
}
