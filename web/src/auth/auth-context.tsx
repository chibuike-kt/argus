import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import * as authApi from "@/api/auth";
import { setUnauthorizedHandler } from "@/api/client";
import type { ApiAuthUser } from "@/api/types";
import { preferences } from "@/lib/preferences";
import { getDrawingsPersistence } from "@/components/chart/drawings/store";

export type AuthStatus = "loading" | "signedIn" | "signedOut";

export interface AuthContextValue {
  status: AuthStatus;
  user: ApiAuthUser | null;
  signIn: (email: string, password: string) => Promise<ApiAuthUser>;
  // completeSignup logs the new account in the same way signIn does --
  // signup/complete itself sets the session cookie (ADR-015), so this
  // is just the matching context-state update, not a second API call.
  completeSignup: (ticket: string, password: string) => Promise<ApiAuthUser>;
  signOut: () => Promise<void>;
  signOutAll: () => Promise<void>;
  refresh: () => Promise<void>;
  // applyUser lets a screen that already has a fresher AuthUser from
  // its own mutation (PUT /me/profile, POST /me/onboarding/complete)
  // push it into context directly, without a second GET /auth/me
  // round trip just to pick up the same response it already has.
  applyUser: (user: ApiAuthUser) => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [user, setUser] = useState<ApiAuthUser | null>(null);
  const navigate = useNavigate();
  const location = useLocation();
  // Read inside the 401 handler below without re-registering it (and
  // so re-running the boot /auth/me call) on every navigation.
  const locationRef = useRef(location);
  locationRef.current = location;

  // Everything the user sets syncs per signed-in user (D1): attach on
  // sign-in or session restore, detach on sign-out so nothing of one
  // user's settings stays in memory for the next.
  const userId = user?.id;
  useEffect(() => {
    if (userId === undefined) {
      if (status === "signedOut") {
        preferences.detach();
        getDrawingsPersistence().attachUser(null);
      }
      return;
    }
    preferences.attach(userId);
    getDrawingsPersistence().attachUser(userId);
  }, [userId, status]);

  useEffect(() => {
    let cancelled = false;

    authApi
      .me()
      .then((u) => {
        if (cancelled) return;
        setUser(u);
        setStatus("signedIn");
      })
      .catch(() => {
        if (cancelled) return;
        setUser(null);
        setStatus("signedOut");
      })
      .finally(() => {
        if (cancelled) return;
        // Registered only after the boot check resolves, so the
        // global handler never fires for that initial "are we signed
        // in at all" probe -- only for a 401 that arrives afterwards,
        // meaning a previously-live session just died.
        setUnauthorizedHandler(() => {
          setUser(null);
          setStatus("signedOut");
          const current = locationRef.current;
          navigate(`/sign-in?next=${encodeURIComponent(current.pathname + current.search)}`, {
            replace: true,
          });
        });
      });

    return () => {
      cancelled = true;
      setUnauthorizedHandler(null);
    };
    // Intentionally runs once: re-reading navigate/location on every render would
    // tear down and rebuild the handler (and re-run the boot call) on
    // every navigation, which is exactly what locationRef above exists
    // to avoid.
  }, []);

  const signIn = async (email: string, password: string) => {
    const u = await authApi.login(email, password);
    setUser(u);
    setStatus("signedIn");
    return u;
  };

  const completeSignup = async (ticket: string, password: string) => {
    const u = await authApi.signupComplete(ticket, password);
    setUser(u);
    setStatus("signedIn");
    return u;
  };

  const signOut = async () => {
    try {
      await authApi.logout();
    } finally {
      setUser(null);
      setStatus("signedOut");
    }
  };

  const signOutAll = async () => {
    try {
      await authApi.logoutAll();
    } finally {
      setUser(null);
      setStatus("signedOut");
    }
  };

  const refresh = async () => {
    try {
      const u = await authApi.me();
      setUser(u);
      setStatus("signedIn");
    } catch {
      setUser(null);
      setStatus("signedOut");
    }
  };

  return (
    <AuthContext.Provider
      value={{ status, user, signIn, completeSignup, signOut, signOutAll, refresh, applyUser: setUser }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}
