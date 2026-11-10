package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/service"
)

func (h *apiHandler) UpdateProfile(ctx context.Context, request UpdateProfileRequestObject) (UpdateProfileResponseObject, error) {
	if request.Body == nil {
		return UpdateProfile400JSONResponse(apiError("invalid_argument", "request body is required")), nil
	}
	ip, userAgent := requestMeta(ctx)
	user, _ := identity.UserFromContext(ctx)
	updated, err := h.auth.UpdateProfile(ctx, user.ID, request.Body.DisplayName, ip, userAgent)
	if err == nil {
		return UpdateProfile200JSONResponse(toAuthUser(updated)), nil
	}
	switch {
	case errors.Is(err, identity.ErrDisplayNameEmpty),
		errors.Is(err, identity.ErrDisplayNameTooLong),
		errors.Is(err, identity.ErrDisplayNameControlRune):
		return UpdateProfile400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: update profile", "user_id", user.ID, "error", err)
	return nil, fmt.Errorf("update profile: %w", err)
}

func (h *apiHandler) CompleteOnboarding(ctx context.Context, _ CompleteOnboardingRequestObject) (CompleteOnboardingResponseObject, error) {
	ip, userAgent := requestMeta(ctx)
	user, _ := identity.UserFromContext(ctx)
	updated, err := h.auth.CompleteOnboarding(ctx, user.ID, ip, userAgent)
	if err == nil {
		return CompleteOnboarding200JSONResponse(toAuthUser(updated)), nil
	}
	if errors.Is(err, service.ErrOnboardingIncomplete) {
		return CompleteOnboarding400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: complete onboarding", "user_id", user.ID, "error", err)
	return nil, fmt.Errorf("complete onboarding: %w", err)
}
