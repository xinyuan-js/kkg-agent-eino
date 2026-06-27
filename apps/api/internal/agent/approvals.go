package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	approvalActionSubmit = "submit_solution"
	approvalReplyApprove = "approve"
	approvalReplyReject  = "reject"
	approvalLifetime     = 10 * time.Minute
)

type storedApproval struct {
	ApprovalRequest
	UserID    int64
	Code      string
	CreatedAt time.Time
}

type approvalStore struct {
	mu    sync.Mutex
	items map[string]storedApproval
}

func (s *approvalStore) save(item storedApproval) ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = make(map[string]storedApproval)
	}
	s.pruneLocked(time.Now())
	s.items[item.ID] = item
	return item.ApprovalRequest
}

func (s *approvalStore) consume(userID int64, sessionID, id string) (storedApproval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		return storedApproval{}, false
	}
	s.pruneLocked(time.Now())
	item, ok := s.items[id]
	if !ok {
		return storedApproval{}, false
	}
	if item.UserID != userID || strings.TrimSpace(item.SessionID) != strings.TrimSpace(sessionID) {
		return storedApproval{}, false
	}
	delete(s.items, id)
	return item, true
}

func (s *approvalStore) pruneLocked(now time.Time) {
	for id, item := range s.items {
		if now.Sub(item.CreatedAt) > approvalLifetime {
			delete(s.items, id)
		}
	}
}

func newApprovalID() string {
	return fmt.Sprintf("approval_%d", time.Now().UnixNano())
}
