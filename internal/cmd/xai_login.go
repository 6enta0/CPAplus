package cmd

import (
	"context"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoXAILogin triggers the OAuth device-code flow and saves the resulting tokens.
func DoXAILogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}
	prompt := options.Prompt
	if prompt == nil {
		prompt = defaultProjectPrompt()
	}
	manager := newAuthManager()
	record, savedPath, errLogin := manager.Login(context.Background(), "xai", cfg, &sdkAuth.LoginOptions{
		NoBrowser: options.NoBrowser,
		Metadata:  map[string]string{},
		Prompt:    prompt,
	})
	if errLogin != nil {
		log.Errorf("xAI authentication failed: %v", errLogin)
		return
	}
	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("xAI authentication successful!")
}
