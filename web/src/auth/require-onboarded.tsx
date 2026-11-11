import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "./auth-context";

// Guards every route under the app shell, after RequireAuth (ADR-015:
// "A signed in user who has not completed onboarding is sent back to
// the step they left") -- a signed-in user whose account has not yet
// finished signup's own display-name/watchlist steps is redirected to
// /onboarding, which resumes at whichever of those two it reads as
// still missing, before this subtree renders anything else.
export function RequireOnboarded() {
  const { user } = useAuth();

  if (user && !user.onboarding_completed) {
    return <Navigate to="/onboarding" replace />;
  }
  return <Outlet />;
}
