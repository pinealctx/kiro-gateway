package kiro

import (
	"context"
	"strings"
	"testing"
)

func TestNewListModelsRequestOmitsEmptyProfileArn(t *testing.T) {
	client := NewCWClient(nil, "ap-southeast-1")
	req, body, err := client.newListModelsRequest(context.Background(), "")
	if err != nil {
		t.Fatalf("newListModelsRequest() error = %v", err)
	}
	if got := req.URL.Host; got != "q.us-east-1.amazonaws.com" {
		t.Fatalf("host = %q, want q.us-east-1.amazonaws.com", got)
	}
	if req.URL.Query().Has("profileArn") {
		t.Fatalf("profileArn query should be omitted for empty profileArn: %s", req.URL.String())
	}
	if strings.Contains(string(body), "profileArn") {
		t.Fatalf("profileArn body should be omitted for empty profileArn: %s", string(body))
	}
}

func TestNewListModelsRequestIncludesProfileArnWhenSet(t *testing.T) {
	client := NewCWClient(nil, "eu-central-1")
	req, body, err := client.newListModelsRequest(context.Background(), "arn:test")
	if err != nil {
		t.Fatalf("newListModelsRequest() error = %v", err)
	}
	if got := req.URL.Host; got != "q.us-east-1.amazonaws.com" {
		t.Fatalf("host = %q, want q.us-east-1.amazonaws.com", got)
	}
	if got := req.URL.Query().Get("profileArn"); got != "arn:test" {
		t.Fatalf("profileArn query = %q, want arn:test", got)
	}
	if !strings.Contains(string(body), `"profileArn":"arn:test"`) {
		t.Fatalf("profileArn body missing: %s", string(body))
	}
}
