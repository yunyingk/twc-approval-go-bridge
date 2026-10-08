//go:build !no_anthropic

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/anthropic/model"
	modelreview "github.com/yunyingk/twc-approval-go-bridge/internal/anthropic/review"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"path/filepath"
	"strings"
)

func newModel(apiKey, baseURL, name string) (invoice.Recognizer, error) {
	return model.New(apiKey, baseURL, name)
}

func newModelReview(cfg config.Config) (appreview.Reviewer, string, string, error) {
	if strings.TrimSpace(cfg.ReviewRulesFile) == "" {
		return nil, "", "", fmt.Errorf("review.rules_file is required for the model provider")
	}
	path := cfg.ReviewRulesFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(cfg.ConfigFile), path)
	}
	rules, err := modelreview.LoadRules(path)
	if err != nil {
		return nil, "", "", err
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return nil, "", "", err
	}
	client, err := modelreview.New(cfg.ReviewModelAPIKey, cfg.ReviewModelBaseURL, cfg.ReviewModelName, rules)
	hash := sha256.Sum256(data)
	return client, rules.Version, cfg.ReviewModelName + ":" + hex.EncodeToString(hash[:]), err
}
