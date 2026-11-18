package passwordreset

// RequestResetRequest is the JSON body accepted by POST /api/auth/forgot-password.
type RequestResetRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest is the JSON body accepted by POST /api/auth/reset-password. NewPassword's
// max length (72) matches bcrypt's own input limit.
type ResetPasswordRequest struct {
	Token       string `json:"token" validate:"required"`
	NewPassword string `json:"newPassword" validate:"required,min=8,max=72"`
}
