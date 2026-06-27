package kkgtools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
)

const (
	SessionKeyAccessToken     = "kkg_access_token"
	SessionKeyUserID          = "kkg_user_id"
	SessionKeyRequestID       = "kkg_request_id"
	SessionKeySubmitConfirmed = "kkg_submit_confirmed"
)

func accessTokenFromContext(ctx context.Context) string {
	value, ok := adk.GetSessionValue(ctx, SessionKeyAccessToken)
	if !ok {
		return ""
	}
	token, _ := value.(string)
	return strings.TrimSpace(token)
}

func userIDFromContext(ctx context.Context) int64 {
	value, ok := adk.GetSessionValue(ctx, SessionKeyUserID)
	if !ok {
		return 0
	}
	switch userID := value.(type) {
	case int64:
		return userID
	case int:
		return int64(userID)
	case float64:
		return int64(userID)
	default:
		return 0
	}
}

func scopedUserID(ctx context.Context, requested int64) (int64, error) {
	current := userIDFromContext(ctx)
	if current <= 0 {
		return requested, nil
	}
	if requested > 0 && requested != current {
		return 0, fmt.Errorf("user_id does not match current session")
	}
	return current, nil
}

func submitConfirmedFromContext(ctx context.Context) bool {
	value, ok := adk.GetSessionValue(ctx, SessionKeySubmitConfirmed)
	if !ok {
		return false
	}
	switch confirmed := value.(type) {
	case bool:
		return confirmed
	case string:
		return strings.EqualFold(strings.TrimSpace(confirmed), "true")
	default:
		return false
	}
}
