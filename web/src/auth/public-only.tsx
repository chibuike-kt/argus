import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "./auth-context";
import { AuthLoadingShell } from "./auth-loading-shell";

// Guards every pre-sign-in route (/sign-in today; /sign-up and
// /onboarding once Part 3 lands): a signed-in user visiting any of
// them is redirected to /setups -- checked before rendering anything
// else, so there is no flash of the form first.
//
// This does NOT wrap children in a shell (it used to wrap everything
// in the dark AuthLayout -- web auth UI brief Part A2 -- before the
// web light auth redesign brief replaced the pre-sign-in pages with a
// white design). Each leaf route now picks its own: AuthShell
// (components/light/auth-shell.tsx) for the new light pages,
// AuthLayout for whatever of the old dark /register, /verify-email
// pair is still standing pending Part 3.
export function PublicOnly() {
  const { status } = useAuth();

  if (status === "loading") return <AuthLoadingShell />;
  if (status === "signedIn") return <Navigate to="/setups" replace />;
  return <Outlet />;
}
