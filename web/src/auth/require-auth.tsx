import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "./auth-context";
import { AuthLoadingShell } from "./auth-loading-shell";
import { safeNextPath } from "./safe-next";

// Guards every route under the app shell (web auth UI brief, Part A2):
// "loading" shows the skeleton shell (never a flash of the login
// page), "signedOut" redirects to /sign-in with the current path as
// `next`, "signedIn" renders the guarded subtree.
export function RequireAuth() {
  const { status } = useAuth();
  const location = useLocation();

  if (status === "loading") return <AuthLoadingShell />;
  if (status === "signedOut") {
    const current = safeNextPath(location.pathname + location.search);
    return <Navigate to={`/sign-in?next=${encodeURIComponent(current)}`} replace />;
  }
  return <Outlet />;
}
