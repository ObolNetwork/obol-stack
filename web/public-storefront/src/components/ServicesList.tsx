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
        const data = await res.json();
        const fresh: Service[] = Array.isArray(data?.services)
          ? data.services
          : [];
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

  // Group into storefront sections by category. Demo is just another
  // category — no special-casing. Services arrive pre-sorted by the catalog
  // (weight desc, then name), so iterating in order and emitting categories
  // as first encountered makes the section order follow weight too
  // (uncategorized services render under "Services").
  const sections = groupByCategory(services);

  return (
    <div className="space-y-6">
      {sections.map(({ category, items }) => (
        <section key={category || "_default"}>
          <h2 className="text-sm font-semibold text-text-muted uppercase tracking-wide mb-3">
            {sectionTitle(category)}
          </h2>
          <div className="space-y-3">
            {items.map((s) => (
              <ServiceCard key={s.name} service={s} />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function groupByCategory(services: Service[]): { category: string; items: Service[] }[] {
  const order: string[] = [];
  const buckets = new Map<string, Service[]>();
  for (const s of services) {
    const cat = s.category ?? "";
    if (!buckets.has(cat)) {
      buckets.set(cat, []);
      order.push(cat);
    }
    buckets.get(cat)!.push(s);
  }
  return order.map((category) => ({ category, items: buckets.get(category)! }));
}

function sectionTitle(category: string): string {
  if (!category) return "Services";
  return category.charAt(0).toUpperCase() + category.slice(1);
}
