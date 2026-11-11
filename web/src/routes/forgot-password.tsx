import { useNavigate } from "react-router-dom";
import { AuthShell } from "@/components/light/auth-shell";

// Password reset itself is not built yet (ADR-015 reserves the shape;
// internal/httpapi pre-lists /auth/forgot-password and
// /auth/reset-password as public, inert until they exist). This page
// says that plainly rather than collecting an email into a form that
// would submit to nothing real -- the same "never pretend a feature
// works" rule sign-in.tsx's own FORGOT_PASSWORD_ENABLED comment
// already follows.
export function ForgotPassword() {
  const navigate = useNavigate();

  return (
    <AuthShell onBack={() => navigate(-1)}>
      <div className="space-y-5">
        <div>
          <h1 className="font-heading text-xl font-bold text-light-text">
            Forgot your password?
          </h1>
          <p className="mt-1 text-sm text-light-muted">
            Password reset isn't available yet. In the meantime, contact support for help getting
            back into your account.
          </p>
        </div>
      </div>
    </AuthShell>
  );
}
