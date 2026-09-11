package telegram_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

func rateLimitedServer(t *testing.T, handler func(attempt int, w http.ResponseWriter)) (*httptest.Server, *int) {
	t.Helper()
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		handler(attempts, w)
	}))
	t.Cleanup(server.Close)
	return server, &attempts
}

func TestSendMessageRetriesOnRateLimit(t *testing.T) {
	server, attempts := rateLimitedServer(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})

	var slept []time.Duration
	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(3), telegram.WithSleeper(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }))

	err := client.Send(context.Background(), 42, "длинный ответ", telegram.SendOptions{})

	require.NoError(t, err, "a 429 must be retried, not dropped")
	assert.Equal(t, 2, *attempts)
	assert.Equal(t, []time.Duration{3 * time.Second}, slept, "must honour retry_after from Telegram")
}

func TestSendMessageGivesUpAfterMaxRetries(t *testing.T) {
	server, attempts := rateLimitedServer(t, func(_ int, w http.ResponseWriter) {
		_, _ = fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
	})

	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(2), telegram.WithSleeper(func(context.Context, time.Duration) error { return nil }))

	err := client.Send(context.Background(), 42, "текст", telegram.SendOptions{})

	require.Error(t, err)
	assert.Equal(t, 3, *attempts, "initial attempt plus two retries")
}

func TestNonRateLimitErrorIsNotRetried(t *testing.T) {
	server, attempts := rateLimitedServer(t, func(_ int, w http.ResponseWriter) {
		_, _ = fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	})

	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(3), telegram.WithSleeper(func(context.Context, time.Duration) error { return nil }))

	err := client.Send(context.Background(), 42, "текст", telegram.SendOptions{})

	require.Error(t, err)
	assert.Equal(t, 1, *attempts, "a 400 is permanent, retrying only wastes time")
}

func TestRateLimitWaitIsCapped(t *testing.T) {
	server, _ := rateLimitedServer(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"slow down","parameters":{"retry_after":100000}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})

	var slept []time.Duration
	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(2), telegram.WithSleeper(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }))

	require.NoError(t, client.Send(context.Background(), 42, "текст", telegram.SendOptions{}))
	require.Len(t, slept, 1)
	assert.LessOrEqual(t, slept[0], 60*time.Second, "an absurd retry_after must be capped")
}

func TestSendMessageRetriesOnServerError(t *testing.T) {
	server, attempts := rateLimitedServer(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "upstream error")
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})

	var slept []time.Duration
	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(3), telegram.WithSleeper(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }))

	err := client.Send(context.Background(), 42, "текст", telegram.SendOptions{})

	require.NoError(t, err, "a transient 5xx must be retried, not dropped")
	assert.Equal(t, 2, *attempts)
	require.Len(t, slept, 1)
	assert.Greater(t, slept[0], time.Duration(0))
}

func TestSendMessageGivesUpAfterMaxRetriesOnServerError(t *testing.T) {
	server, attempts := rateLimitedServer(t, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "upstream error")
	})

	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(2), telegram.WithSleeper(func(context.Context, time.Duration) error { return nil }))

	err := client.Send(context.Background(), 42, "текст", telegram.SendOptions{})

	require.Error(t, err)
	assert.Equal(t, 3, *attempts, "initial attempt plus two retries, then give up")
}

func TestSendMessageRetriesOnTransportError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			hj, ok := w.(http.Hijacker)
			require.True(t, ok, "test server must support hijacking to simulate a dropped connection")
			conn, _, err := hj.Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(server.Close)

	var slept []time.Duration
	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(3), telegram.WithSleeper(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }))

	err := client.Send(context.Background(), 42, "текст", telegram.SendOptions{})

	require.NoError(t, err, "a network-level failure must be retried, not dropped")
	assert.Equal(t, 2, attempts)
	require.Len(t, slept, 1)
	assert.Greater(t, slept[0], time.Duration(0))
}

func TestRateLimitWithoutRetryAfterStillWaits(t *testing.T) {
	server, _ := rateLimitedServer(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})

	var slept []time.Duration
	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(2), telegram.WithSleeper(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }))

	require.NoError(t, client.Send(context.Background(), 42, "текст", telegram.SendOptions{}))
	require.Len(t, slept, 1)
	assert.Greater(t, slept[0], time.Duration(0), "a 429 without retry_after should still back off")
}

func TestCancellingTheContextAbortsTheRetryWait(t *testing.T) {
	server, attempts := rateLimitedServer(t, func(_ int, w http.ResponseWriter) {
		_, _ = fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 60","parameters":{"retry_after":60}}`)
	})

	client := telegram.NewClient("test-token",
		telegram.WithBaseURL(server.URL),
		telegram.WithMaxRetries(3))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	err := client.Send(ctx, 42, "текст", telegram.SendOptions{})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled, "the abort reason must survive, not be hidden behind the 429")
	assert.Contains(t, err.Error(), "Too Many Requests", "the error that triggered the retry must still be readable")
	assert.Less(t, elapsed, 5*time.Second, "a cancelled context must not wait out the 60s retry_after")
	assert.Equal(t, 1, *attempts, "no further request may be sent after cancellation")
}
