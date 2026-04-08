"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";

const navItems = [
  { label: "Dashboard", href: "/" },
  { label: "Projects", href: "/projects" },
  { label: "Analyses", href: "/analyses" },
  { label: "Findings", href: "/findings" },
  { label: "Groups", href: "/groups" },
  { label: "Info", href: "/settings" },
];

const adminItems = [
  { label: "Users", href: "/admin/users" },
  { label: "API Keys", href: "/admin/api-keys" },
  { label: "AUP", href: "/admin/aup" },
  { label: "Backups", href: "/admin/backups" },
  { label: "Logs", href: "/admin/logs" },
  { label: "Settings", href: "/admin/settings" },
];

interface SidebarProps {
  roles?: string[];
  userName?: string;
}

export function Sidebar({ roles = [], userName }: SidebarProps) {
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  const isAdmin = roles.includes("admin");

  return (
    <>
      {/* Mobile toggle */}
      <button
        className="lg:hidden fixed top-4 left-4 z-50 p-2 bg-gray-800 text-white rounded print:hidden"
        onClick={() => setMobileOpen(!mobileOpen)}
      >
        ☰
      </button>

      {/* Backdrop */}
      {mobileOpen && (
        <div
          className="lg:hidden fixed inset-0 bg-black/50 z-30"
          onClick={() => setMobileOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside
        className={`
          fixed lg:static inset-y-0 left-0 z-40
          w-64 bg-gray-900 text-white
          transform transition-transform lg:translate-x-0 print:hidden
          ${mobileOpen ? "translate-x-0" : "-translate-x-full"}
        `}
      >
        <div className="p-4 border-b border-gray-700">
          <Link href="/" className="text-xl font-bold">
            SWAMP
          </Link>
          <p className="text-xs text-gray-400 mt-1">
            Security Analysis Platform
          </p>
        </div>

        <nav className="p-4 space-y-1">
          {navItems.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className={`block px-3 py-2 rounded text-sm ${
                pathname === item.href
                  ? "bg-blue-600 text-white"
                  : "text-gray-300 hover:bg-gray-800"
              }`}
              onClick={() => setMobileOpen(false)}
            >
              {item.label}
            </Link>
          ))}

          {isAdmin && (
            <div className="pt-4 mt-4 border-t border-gray-700">
              <p className="px-3 text-xs text-gray-500 uppercase mb-2">Admin</p>
              {adminItems.map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  className={`block px-3 py-2 rounded text-sm ${
                    pathname === item.href
                      ? "bg-blue-600 text-white"
                      : "text-gray-300 hover:bg-gray-800"
                  }`}
                  onClick={() => setMobileOpen(false)}
                >
                  {item.label}
                </Link>
              ))}
            </div>
          )}
        </nav>

        <div className="absolute bottom-0 left-0 right-0 p-4 border-t border-gray-700">
          {userName && (
            <div className="px-3 pb-2">
              <p className="text-sm text-gray-200 truncate">{userName}</p>
              {roles.length > 0 && (
                <div className="flex flex-wrap gap-1 mt-1">
                  {roles.map((role) => (
                    <span
                      key={role}
                      className="text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-400"
                    >
                      {role}
                    </span>
                  ))}
                </div>
              )}
            </div>
          )}
          <button
            onClick={() => {
              fetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
                window.location.href = "/";
              });
            }}
            className="w-full text-left px-3 py-2 text-sm text-gray-400 hover:text-white"
          >
            Sign Out
          </button>
        </div>
      </aside>
    </>
  );
}
