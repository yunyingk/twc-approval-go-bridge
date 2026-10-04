//go:build !no_anthropic

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/yunyingk/twc-approval-go-bridge/internal/anthropic/model"
	modelreview "github.com/yunyingk/twc-approval-go-bridge/internal/anthropic/review"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"os"
)

func newModel(apiKey, baseURL, name string) (invoice.Recognizer, error) {
	return model.New(apiKey, baseURL, name)
}

func newModelReview(cfg config.Config) (appreview.Reviewer, string, string, error) {
	data, err := os.ReadFile(cfg.ReviewRulesFile)
	if err != nil {
		return nil, "", "", err
	}
	var rules modelreview.Rules
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, "", "", err
	}
	client, err := modelreview.New(cfg.ReviewModelAPIKey, cfg.ReviewModelBaseURL, cfg.ReviewModelName, rules)
	hash := sha256.Sum256(data)
	return client, rules.Version, cfg.ReviewModelName + ":" + hex.EncodeToString(hash[:]), err
}
