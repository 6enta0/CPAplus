package gemini

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const interactionsAgentAuthSelectionModel = "gemini-2.5-flash"

// InteractionsRequestTarget is the validated execution target for an Interactions request.
type InteractionsRequestTarget struct {
	Model  string
	Agent  string
	Stream bool
}

// ParseInteractionsRequestTarget validates the mutually exclusive target fields and stream type.
func ParseInteractionsRequestTarget(rawJSON []byte) (InteractionsRequestTarget, error) {
	if !gjson.ValidBytes(rawJSON) {
		return InteractionsRequestTarget{}, fmt.Errorf("invalid JSON body")
	}
	root := gjson.ParseBytes(rawJSON)
	model := strings.TrimSpace(root.Get("model").String())
	agent := strings.TrimSpace(root.Get("agent").String())
	if (model == "") == (agent == "") {
		return InteractionsRequestTarget{}, fmt.Errorf("request requires exactly one of model or agent")
	}
	stream := false
	if streamNode := root.Get("stream"); streamNode.Exists() {
		if !streamNode.IsBool() {
			return InteractionsRequestTarget{}, fmt.Errorf("stream must be a boolean")
		}
		stream = streamNode.Bool()
	}
	return InteractionsRequestTarget{Model: model, Agent: agent, Stream: stream}, nil
}

// NormalizeInteractionsModelResourceName removes the Gemini REST models/ prefix.
func NormalizeInteractionsModelResourceName(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasPrefix(model, "models/") && len(model) > len("models/") {
		return strings.TrimPrefix(model, "models/")
	}
	return model
}

// InteractionsExecutionMetadata records protocol and selection information for later executors.
type InteractionsExecutionMetadata struct {
	EntryProtocol      string
	ExitProtocol       string
	Model              string
	Agent              string
	AuthSelectionModel string
	Stream             bool
}

func prepareInteractionsExecution(rawJSON []byte, target InteractionsRequestTarget) (string, []byte, InteractionsExecutionMetadata) {
	model := target.Model
	if target.Agent != "" {
		model = interactionsAgentAuthSelectionModel
	}
	normalized := NormalizeInteractionsModelResourceName(model)
	if target.Agent == "" && normalized != model {
		if updated, errSet := sjson.SetBytes(rawJSON, "model", normalized); errSet == nil {
			rawJSON = updated
		}
	}
	return normalized, rawJSON, InteractionsExecutionMetadata{
		EntryProtocol:      Interactions,
		ExitProtocol:       Interactions,
		Model:              normalized,
		Agent:              target.Agent,
		AuthSelectionModel: normalized,
		Stream:             target.Stream,
	}
}

// Interactions handles POST /v1beta/interactions.
func (h *GeminiAPIHandler) Interactions(c *gin.Context) {
	rawJSON, errRead := c.GetRawData()
	if errRead != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{Error: handlers.ErrorDetail{Message: errRead.Error(), Type: "invalid_request_error"}})
		return
	}
	target, errParse := ParseInteractionsRequestTarget(rawJSON)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{Error: handlers.ErrorDetail{Message: errParse.Error(), Type: "invalid_request_error"}})
		return
	}
	modelName, rawJSON, _ := prepareInteractionsExecution(rawJSON, target)
	alt := h.GetAlt(c)
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, c.Request.Context())
	defer cliCancel(nil)
	if target.Stream {
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{Error: handlers.ErrorDetail{Message: "Streaming not supported", Type: "server_error"}})
			return
		}
		dataChan, upstreamHeaders, errChan := h.ExecuteStreamWithAuthManager(cliCtx, Interactions, modelName, rawJSON, alt)
		for key, values := range upstreamHeaders {
			for _, value := range values {
				c.Writer.Header().Add(key, value)
			}
		}
		h.forwardGeminiStream(c, flusher, alt, func(err error) { cliCancel(err) }, dataChan, errChan)
		_ = upstreamHeaders
		return
	}
	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, Interactions, modelName, rawJSON, alt)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	for key, values := range upstreamHeaders {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	c.Data(http.StatusOK, "application/json", resp)
}
