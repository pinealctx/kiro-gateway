package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pinealctx/kiro-gateway/core/logutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const maxDebugBodyLogBytes = 8 * 1024

type bodyLogWriter struct {
	gin.ResponseWriter
	buf       bytes.Buffer
	limit     int
	truncated bool
	total     int
}

func (w *bodyLogWriter) Write(b []byte) (int, error) {
	w.total += len(b)
	if !w.truncated && w.limit > 0 {
		remain := w.limit - w.buf.Len()
		if remain > 0 {
			if len(b) > remain {
				_, _ = w.buf.Write(b[:remain])
				w.truncated = true
			} else {
				_, _ = w.buf.Write(b)
			}
		} else {
			w.truncated = true
		}
	}
	return w.ResponseWriter.Write(b)
}

// RequestID injects a unique request ID into the context and response headers.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// Logger logs each request with latency, status, method, path.
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !shouldLogRequest(path) {
			c.Next()
			return
		}

		debugEnabled := logger.Core().Enabled(zapcore.DebugLevel)

		start := time.Now()
		method := c.Request.Method

		var reqBodyRaw string
		var reqBodyTruncated bool
		var reqBodyTotalLen int

		if debugEnabled && c.Request.Body != nil {
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err == nil {
				reqBodyTotalLen = len(bodyBytes)
				reqBodyRaw = string(bodyBytes)
				reqBodyRaw, reqBodyTruncated = logutil.TruncateString(reqBodyRaw, maxDebugBodyLogBytes)
				c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			} else {
				c.Request.Body = io.NopCloser(bytes.NewReader(nil))
			}
		}

		blw := &bodyLogWriter{ResponseWriter: c.Writer, limit: maxDebugBodyLogBytes}
		c.Writer = blw

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		reqID, _ := c.Get("request_id")
		if reqID == nil {
			reqID = ""
		}
		apiKeyID, _ := c.Get(CtxKeyTenantID)
		if apiKeyID == nil {
			apiKeyID = ""
		}
		apiKeyName, _ := c.Get(CtxKeyTenantName)
		if apiKeyName == nil {
			apiKeyName = ""
		}

		if debugEnabled {
			reqPayload := logutil.RedactString(reqBodyRaw)
			reqPayload = logutil.WithTruncationSuffix(reqPayload, reqBodyTruncated, reqBodyTotalLen, maxDebugBodyLogBytes)

			respPayload := logutil.RedactString(blw.buf.String())
			respPayload = logutil.WithTruncationSuffix(respPayload, blw.truncated, blw.total, maxDebugBodyLogBytes)

			logger.Debug("http raw traffic",
				zap.String("request_id", reqID.(string)),
				zap.String("api_key_id", apiKeyID.(string)),
				zap.String("api_key_name", apiKeyName.(string)),
				zap.String("method", method),
				zap.String("path", path),
				zap.Int("status", status),
				zap.Duration("latency", latency),
				zap.String("client_ip", c.ClientIP()),
				zap.Any("request_headers", logutil.RedactHeaders(c.Request.Header)),
				zap.String("request_body", reqPayload),
				zap.Any("response_headers", logutil.RedactHeaders(c.Writer.Header())),
				zap.String("response_body", respPayload),
			)
			return
		}

		if shouldInfoLogRequest(path) {
			logger.Info("runtime request",
				zap.String("request_id", reqID.(string)),
				zap.String("api_key_id", apiKeyID.(string)),
				zap.String("api_key_name", apiKeyName.(string)),
				zap.Int("status", status),
				zap.String("method", method),
				zap.String("path", path),
				zap.Duration("latency", latency),
				zap.String("client_ip", c.ClientIP()),
			)
		}
	}
}

func shouldLogRequest(path string) bool {
	return path == "/v1" ||
		strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/a/") ||
		path == "/admin" ||
		strings.HasPrefix(path, "/admin/")
}

func shouldInfoLogRequest(path string) bool {
	return path == "/v1" ||
		strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/a/")
}
