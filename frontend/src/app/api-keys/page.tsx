"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function APIKeysPage() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/admin/api-keys");
  }, [router]);
  return <p>Redirecting...</p>;
}
