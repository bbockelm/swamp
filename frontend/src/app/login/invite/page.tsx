"use client";

import { Suspense, useState, useEffect } from "react";
import { useSearchParams } from "next/navigation";

export default function InvitePage() {
  return (
    <Suspense fallback={<div className="p-8">Loading...</div>}>
      <InviteContent />
    </Suspense>
  );
}

function InviteContent() {
  const searchParams = useSearchParams();
  const token = searchParams.get("token");
  const [authMode, setAuthMode] = useState<string>("");
  const [loading, setLoading] = useState(!!token); // Only load if we have a token
  const [error, setError] = useState<string>("");

  useEffect(() => {
    if (!token) {
      // No token - nothing to fetch
      return;
    }

    let cancelled = false;
    fetch("/api/v1/auth/mode")
      .then((r) => r.json())
      .then((data) => {
        if (!cancelled) {
          setAuthMode(data.mode);
          setLoading(false);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setError("Failed to check authentication mode");
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [token]);

  const handleLogin = () => {
    // Redirect to OIDC login with the user invite token
    window.location.href = `/api/v1/auth/oidc/login?user_invite_token=${encodeURIComponent(token || "")}`;
  };

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100">
        <div className="text-gray-500">Loading...</div>
      </div>
    );
  }

  if (error || !token) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100">
        <div className="bg-white p-8 rounded-lg shadow-md w-full max-w-md">
          <h1 className="text-2xl font-bold text-center mb-2 text-red-600">
            Invalid Invite
          </h1>
          <p className="text-center text-gray-600">
            {error || "Missing invite token."}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-100">
      <div className="bg-white p-8 rounded-lg shadow-md w-full max-w-md">
        <h1 className="text-2xl font-bold text-center mb-2">SWAMP</h1>
        <p className="text-center text-gray-500 mb-6">
          Link Your Identity
        </p>

        <div className="bg-blue-50 border border-blue-200 p-4 rounded mb-6">
          <p className="text-sm text-blue-800">
            You&apos;ve been invited to link a new identity to your SWAMP account.
            Click the button below to authenticate with your identity provider.
          </p>
        </div>

        {authMode === "dev" ? (
          <div className="space-y-4">
            <div className="bg-yellow-50 border border-yellow-200 p-3 rounded text-sm text-yellow-800">
              Development mode — user invite linking is not available in dev mode.
            </div>
          </div>
        ) : (
          <button
            onClick={handleLogin}
            className="w-full bg-blue-600 text-white py-2 rounded hover:bg-blue-700"
          >
            Link Identity with CILogon
          </button>
        )}
      </div>
    </div>
  );
}
