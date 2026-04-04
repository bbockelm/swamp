"use client";

import { Suspense, useState, useEffect } from "react";
import { useSearchParams } from "next/navigation";

export default function LoginPage() {
  return (
    <Suspense fallback={<div className="p-8">Loading...</div>}>
      <LoginContent />
    </Suspense>
  );
}

function LoginContent() {
  const searchParams = useSearchParams();
  const errorMsg = searchParams.get("error");
  const [authMode, setAuthMode] = useState<string>("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch("/api/v1/auth/mode")
      .then((r) => r.json())
      .then((data) => {
        setAuthMode(data.mode);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  const handleOIDCLogin = () => {
    const returnTo = searchParams.get("return_to") || "/";
    window.location.href = `/api/v1/auth/oidc/login?return_to=${encodeURIComponent(returnTo)}`;
  };

  if (loading) return <div className="p-8">Loading...</div>;

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-100">
      <div className="bg-white p-8 rounded-lg shadow-md w-full max-w-md">
        <h1 className="text-2xl font-bold text-center mb-2">SWAMP</h1>
        <p className="text-center text-gray-500 mb-6">
          Software Assurance Marketplace
        </p>

        {errorMsg && (
          <div className="bg-red-50 border border-red-200 p-3 rounded text-sm text-red-700 mb-4">
            {errorMsg}
          </div>
        )}

        {authMode === "dev" ? (
          <div className="space-y-4">
            <div className="bg-yellow-50 border border-yellow-200 p-3 rounded text-sm text-yellow-800">
              Development mode — use the login link printed to the server terminal.
            </div>
            <p className="text-sm text-gray-600 text-center">
              Look for the <span className="font-mono">Dev login URL</span> in the server output.
            </p>
          </div>
        ) : (
          <div className="text-center">
            <p className="text-sm text-gray-600 mb-4">
              Sign in with your institutional credentials via CILogon.
            </p>
            <button
              onClick={handleOIDCLogin}
              className="w-full bg-blue-600 text-white py-2 rounded hover:bg-blue-700"
            >
              Sign In with CILogon
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
