package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"golang.org/x/net/context"
)

// ProtocolExecutionRequest describes one route-level request with explicit entry and exit formats.
// ForcedProvider and AuthSelectionModel are used by provider-native targets such as Interactions agents.
type ProtocolExecutionRequest struct {
	EntryProtocol      string
	ExitProtocol       string
	ForcedProvider     string
	AuthSelectionModel string
	Model              string
	Stream             bool
	Body               []byte
	Alt                string
}

// ProtocolExecutionResponse is the non-streaming result of a protocol execution.
type ProtocolExecutionResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// ProtocolExecutionStream is the streaming result of a protocol execution.
type ProtocolExecutionStream struct {
	StatusCode int
	Headers    http.Header
	Chunks     <-chan []byte
	Errors     <-chan *interfaces.ErrorMessage
}

// ExecuteProtocolWithAuthManager executes a route-level non-streaming request.
func (h *BaseAPIHandler) ExecuteProtocolWithAuthManager(ctx context.Context, req ProtocolExecutionRequest) (ProtocolExecutionResponse, *interfaces.ErrorMessage) {
	if req.Stream {
		return ProtocolExecutionResponse{}, protocolExecutionModeError("ExecuteProtocolWithAuthManager requires Stream=false")
	}
	providers, normalizedModel, errMsg := h.resolveProtocolExecution(req)
	if errMsg != nil {
		return ProtocolExecutionResponse{}, errMsg
	}
	body, headers, errMsg := h.executeWithAuthManagerResolved(ctx, req.EntryProtocol, req.Model, normalizedModel, providers, req.Body, req.Alt, req.AuthSelectionModel)
	if errMsg != nil {
		return ProtocolExecutionResponse{}, errMsg
	}
	return ProtocolExecutionResponse{StatusCode: http.StatusOK, Headers: headers, Body: body}, nil
}

// ExecuteProtocolStreamWithAuthManager executes a route-level streaming request.
func (h *BaseAPIHandler) ExecuteProtocolStreamWithAuthManager(ctx context.Context, req ProtocolExecutionRequest) (ProtocolExecutionStream, *interfaces.ErrorMessage) {
	if !req.Stream {
		return ProtocolExecutionStream{}, protocolExecutionModeError("ExecuteProtocolStreamWithAuthManager requires Stream=true")
	}
	providers, normalizedModel, errMsg := h.resolveProtocolExecution(req)
	if errMsg != nil {
		return ProtocolExecutionStream{}, errMsg
	}
	chunks, headers, errs := h.executeStreamWithAuthManagerResolved(ctx, req.EntryProtocol, req.Model, normalizedModel, providers, req.Body, req.Alt, req.AuthSelectionModel)
	return ProtocolExecutionStream{StatusCode: http.StatusOK, Headers: headers, Chunks: chunks, Errors: errs}, nil
}

func (h *BaseAPIHandler) resolveProtocolExecution(req ProtocolExecutionRequest) ([]string, string, *interfaces.ErrorMessage) {
	entryProtocol := strings.TrimSpace(req.EntryProtocol)
	exitProtocol := strings.TrimSpace(req.ExitProtocol)
	if entryProtocol == "" || (exitProtocol != "" && !strings.EqualFold(entryProtocol, exitProtocol)) {
		return nil, "", &interfaces.ErrorMessage{StatusCode: http.StatusBadRequest, Error: errors.New("entry and exit protocols must match")}
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, "", &interfaces.ErrorMessage{StatusCode: http.StatusBadRequest, Error: errors.New("model is required")}
	}
	if forcedProvider := strings.TrimSpace(req.ForcedProvider); forcedProvider != "" {
		return []string{forcedProvider}, model, nil
	}
	return h.getRequestDetails(entryProtocol, model)
}

func protocolExecutionModeError(message string) *interfaces.ErrorMessage {
	return &interfaces.ErrorMessage{StatusCode: http.StatusBadRequest, Error: errors.New(message)}
}
