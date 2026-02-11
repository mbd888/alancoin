package gateway

import "context"

// Store persists gateway session data.
type Store interface {
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	ListSessions(ctx context.Context, agentAddr string, limit int) ([]*Session, error)

	CreateLog(ctx context.Context, log *RequestLog) error
	ListLogs(ctx context.Context, sessionID string, limit int) ([]*RequestLog, error)
}
