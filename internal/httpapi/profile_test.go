package httpapi

import (
	"context"
	"testing"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/service"
)

func TestCompleteOnboarding_Incomplete_Maps409(t *testing.T) {
	h := newTestHandler(&fakeAuthService{completeOnboardingErr: service.ErrOnboardingIncomplete})
	ctx := identity.WithUser(context.Background(), identity.User{ID: 1})

	resp, err := h.CompleteOnboarding(ctx, CompleteOnboardingRequestObject{})
	if err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}
	errResp, ok := resp.(CompleteOnboarding409JSONResponse)
	if !ok {
		t.Fatalf("CompleteOnboarding returned %T, want CompleteOnboarding409JSONResponse", resp)
	}
	if errResp.Error.Code != "onboarding_incomplete" {
		t.Fatalf("error code = %q, want onboarding_incomplete", errResp.Error.Code)
	}
}
