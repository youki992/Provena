package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/chobits02/provena/internal/config"
)

func TestPreparePayloadWithGlobalSystemPromptPrependsToExistingSystem(t *testing.T) {
	client := NewClient(&config.OpenAIConfig{GlobalSystemPrompt: "global"}, nil, nil)
	payload := map[string]interface{}{
		"model": "test-model",
		"messages": []map[string]string{
			{"role": "system", "content": "local"},
			{"role": "user", "content": "hello"},
		},
	}

	prepared, err := client.preparePayloadWithGlobalSystemPrompt(payload)
	if err != nil {
		t.Fatalf("prepare payload: %v", err)
	}
	obj := prepared.(map[string]interface{})
	messages := obj["messages"].([]interface{})
	first := messages[0].(map[string]interface{})
	if first["content"] != "global\n\nlocal" {
		t.Fatalf("first system content = %q", first["content"])
	}
	if messages[1].(map[string]interface{})["role"] != "user" {
		t.Fatalf("user message moved or changed: %#v", messages[1])
	}
}

func TestPreparePayloadWithGlobalSystemPromptInsertsSystemBeforeUser(t *testing.T) {
	client := NewClient(&config.OpenAIConfig{GlobalSystemPrompt: "global"}, nil, nil)
	payload := map[string]interface{}{
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}

	prepared, err := client.preparePayloadWithGlobalSystemPrompt(payload)
	if err != nil {
		t.Fatalf("prepare payload: %v", err)
	}
	messages := prepared.(map[string]interface{})["messages"].([]interface{})
	first := messages[0].(map[string]interface{})
	if first["role"] != "system" || first["content"] != "global" {
		t.Fatalf("first message = %#v", first)
	}
}

func TestPreparePayloadWithGlobalSystemPromptDoesNotMutatePayload(t *testing.T) {
	client := NewClient(&config.OpenAIConfig{GlobalSystemPrompt: "global"}, nil, nil)
	payload := map[string]interface{}{
		"messages": []map[string]string{{"role": "system", "content": "local"}},
	}
	if _, err := client.preparePayloadWithGlobalSystemPrompt(payload); err != nil {
		t.Fatalf("prepare payload: %v", err)
	}
	if payload["messages"].([]map[string]string)[0]["content"] != "local" {
		t.Fatalf("original payload was mutated: %#v", payload)
	}
}

func TestGlobalSystemPromptRoundTripperRewritesEinoRequest(t *testing.T) {
	var captured []byte
	rt := &globalSystemPromptRoundTripper{
		cfg: &config.OpenAIConfig{GlobalSystemPrompt: "global"},
		base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			var err error
			captured, err = io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
				Request:    req,
			}, nil
		}),
	}
	req := &http.Request{
		Method: "POST",
		URL:    &url.URL{Scheme: "http", Host: "example.test", Path: "/v1/chat/completions"},
		Body:   io.NopCloser(bytes.NewReader([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))),
	}
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(captured, &envelope); err != nil {
		t.Fatalf("captured request is not JSON: %v", err)
	}
	messages := envelope["messages"].([]interface{})
	first := messages[0].(map[string]interface{})
	if first["role"] != "system" || first["content"] != "global" {
		t.Fatalf("first message = %#v", first)
	}
}
