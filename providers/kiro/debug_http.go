package kiro

import (
	"net/http"

	"github.com/pinealctx/kiro-gateway/core/logutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func debugKiroHTTPRequest(logger *zap.Logger, msg string, req *http.Request, body []byte) {
	if logger == nil || !logger.Core().Enabled(zapcore.DebugLevel) {
		return
	}
	reqBody, truncated := logutil.TruncateString(logutil.RedactString(string(body)), maxLogBody)
	reqBody = logutil.WithTruncationSuffix(reqBody, truncated, len(body), maxLogBody)
	logger.Debug(msg,
		zap.String("url", req.URL.String()),
		zap.String("method", req.Method),
		zap.Any("headers", logutil.RedactHeaders(req.Header)),
		zap.String("request_body", reqBody),
	)
}

func debugKiroHTTPResponse(logger *zap.Logger, msg string, resp *http.Response, body []byte) {
	if logger == nil || !logger.Core().Enabled(zapcore.DebugLevel) {
		return
	}
	respBody, truncated := logutil.TruncateString(logutil.RedactString(string(body)), maxLogBody)
	respBody = logutil.WithTruncationSuffix(respBody, truncated, len(body), maxLogBody)
	logger.Debug(msg,
		zap.Int("status", resp.StatusCode),
		zap.Any("headers", logutil.RedactHeaders(resp.Header)),
		zap.String("response_body", respBody),
	)
}
