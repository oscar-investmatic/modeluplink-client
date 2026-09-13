package main

import (
	"errors"
	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
	"runtime"
	"strings"
)

type setupProgress struct {
	Event     string `json:"event"`
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	Completed int64  `json:"completed,omitempty"`
	Total     int64  `json:"total,omitempty"`
}
type setupReporter func(setupProgress)

func (report setupReporter) stage(stage, message string) {
	if report != nil {
		report(setupProgress{Event: "progress", Stage: stage, Message: message})
	}
}
func setupError(stage string, err error) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code == "invalid_region" {
			return errors.New("Your model is ready on this Mac, but the connection service has no available region. Please try again later. Your model does not need to download again.")
		}
		return errors.New(desktopError(err))
	}
	switch stage {
	case "modelcheck":
		return errors.New("Your model is installed but couldn’t answer on this Mac. Choose a smaller model or free some memory, then try again.")
	case "download":
		if strings.Contains(strings.ToLower(err.Error()), "no space left") {
			return errors.New("Your Mac ran out of space while downloading the model. Free some disk space, then connect again.")
		}
		return errors.New("We couldn’t finish downloading your model. Check your internet connection, then try again. Ollama will reuse files already downloaded.")
	case "prepare":
		return errors.New("We couldn’t start Ollama on this Mac. Open Ollama, then try connecting again.")
	case "reserve":
		return errors.New("Your model is ready on this Mac, but we couldn’t reserve its address. Check your connection and try again; the model is already downloaded.")
	case "start":
		if runtime.GOOS == "windows" {
			return errors.New("We couldn’t start background sharing. Check that Task Scheduler is available, then try again.")
		}
		return errors.New("We couldn’t start the background connection. Check that Model Uplink is allowed in System Settings → General → Login Items, then try again.")
	default:
		return errors.New("We couldn’t finish checking this Mac. Please try again.")
	}
}
