"use client";

import { useEffect, useState } from "react";
import type { Service } from "@/types";
import { ServiceCard } from "./ServiceCard";

const REFRESH_INTERVAL_MS = 10_000;

export function ServicesList({ initial }: { initial: Service[] }) {
  const [services, setServices] = useState<Service[]>(initial);

  useEffect(() => {
    let cancelled = false;
    const tick = async () => {
      try {
        const res = await fetch("/api/services.json", { cache: "no-store" });
        if (!res.ok) return;
        const fresh: Service[] = await res.json();
        if (!cancelled) setServices(fresh);
      } catch {
        // Network blip — keep existing list, retry next tick.
      }
    };
    const id = setInterval(tick, REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  if (services.length === 0) {
    return (
      <div className="rounded-lg border border-stroke bg-bg02 p-10 text-center">
        <p className="text-text-body mb-2">
          No services are currently available.
        </p>
        <p className="text-sm text-text-muted">
          Run{" "}
          <code className="px-2 py-1 rounded bg-bg01 border border-stroke text-obol-green font-mono text-xs">
            obol sell demo
          </code>{" "}
          to deploy your first demo service.
        </p>
      </div>
    );
  }

  const demos = services.filter((s) => s.isDemo);
  const others = services.filter((s) => !s.isDemo);

  return (
    <div className="space-y-6">
      {demos.length > 0 && (
        <section>
          <h2 className="text-sm font-semibold text-text-muted uppercase tracking-wide mb-3">
            Demo services
          </h2>
          <div className="space-y-3">
            {demos.map((s) => (
              <ServiceCard key={s.name} service={s} />
            ))}
          </div>
        </section>
      )}

      {others.length > 0 && (
        <section>
          <h2 className="text-sm font-semibold text-text-muted uppercase tracking-wide mb-3">
            Services
          </h2>
          <div className="space-y-3">
            {others.map((s) => (
              <ServiceCard key={s.name} service={s} />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
