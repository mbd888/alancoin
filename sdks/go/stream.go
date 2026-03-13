package alancoin

import (
	"context"
	"fmt"
	"net/http"
)

// OpenStream opens a new streaming micropayment channel.
func (c *Client) OpenStream(ctx context.Context, req OpenStreamRequest) (*Stream, error) {
	var out streamResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/streams", &req, &out); err != nil {
		return nil, err
	}
	return &out.Stream, nil
}

// GetStream retrieves a stream by ID.
func (c *Client) GetStream(ctx context.Context, streamID string) (*Stream, error) {
	var out streamResponse
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/streams/%s", streamID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Stream, nil
}

// TickStream records a micropayment tick on a stream.
func (c *Client) TickStream(ctx context.Context, streamID string, req TickStreamRequest) (*StreamTick, *Stream, error) {
	var out tickResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/streams/%s/tick", streamID), &req, &out); err != nil {
		return nil, nil, err
	}
	return &out.Tick, &out.Stream, nil
}

// CloseStream closes a stream and settles the final amounts.
func (c *Client) CloseStream(ctx context.Context, streamID, reason string) (*Stream, error) {
	var body any
	if reason != "" {
		body = &struct {
			Reason string `json:"reason"`
		}{Reason: reason}
	}
	var out streamResponse
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/streams/%s/close", streamID), body, &out); err != nil {
		return nil, err
	}
	return &out.Stream, nil
}

// ListStreamTicks retrieves ticks for a stream.
func (c *Client) ListStreamTicks(ctx context.Context, streamID string, limit int) ([]StreamTick, error) {
	l := "100"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/streams/%s/ticks", streamID) + buildQuery("limit", l)
	var out listTicksResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Ticks, nil
}

// ListStreams retrieves streams for an agent.
func (c *Client) ListStreams(ctx context.Context, agentAddr string, limit int) ([]Stream, error) {
	l := "50"
	if limit > 0 {
		l = fmt.Sprintf("%d", limit)
	}
	path := fmt.Sprintf("/v1/agents/%s/streams", agentAddr) + buildQuery("limit", l)
	var out listStreamsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Streams, nil
}
