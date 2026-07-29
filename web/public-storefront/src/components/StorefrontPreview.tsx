"use client";

import { useEffect, useState } from "react";
import { Header } from "@/components/Header";
import { PaymentFlow } from "@/components/PaymentFlow";
import { RichText } from "@/components/RichText";
import { ServicesList } from "@/components/ServicesList";
import type { ServiceCatalogDocument } from "@/lib/catalog";
import { isDarkTheme, themeStyle } from "@/lib/theme";

const DEFAULT_HERO_TITLE = "Agent services";
const DEFAULT_LOGO_PATH = "/obol-stack-logo.png";
const PREVIEW_MESSAGE = "obol.storefront.preview";
const PREVIEW_READY_MESSAGE = "obol.storefront.preview.ready";
const HEX_RE = /^#[0-9a-fA-F]{3,8}$/;
const MAX_DATA_URL_LENGTH = 360_000;
const ALLOWED_OPERATOR_HOSTS = new Set(["obol.stack", "localhost", "127.0.0.1"]);

interface PreviewBranding {
  displayName: string;
  tagline: string;
  theme: "light" | "dark" | "obol";
  themeVars: Record<string, string>;
  logoUrl: string | null;
}

function operatorOrigin(): string | null {
  if (!document.referrer) return null;
  try {
    const url = new URL(document.referrer);
    return ALLOWED_OPERATOR_HOSTS.has(url.hostname) ? url.origin : null;
  } catch {
    return null;
  }
}

function optionalImage(value: unknown): string | null | undefined {
  if (value === null) return null;
  if (typeof value !== "string" || value.length > MAX_DATA_URL_LENGTH) {
    return undefined;
  }
  if (
    /^data:image\/[^;,]+;base64,[a-zA-Z0-9+/=\s]+$/.test(value) ||
    value.startsWith("https://") ||
    value.startsWith("http://") ||
    value.startsWith("/")
  ) {
    return value;
  }
  return undefined;
}

function parsePreviewMessage(data: unknown): PreviewBranding | null {
  if (!data || typeof data !== "object") return null;
  const message = data as Record<string, unknown>;
  if (message.type !== PREVIEW_MESSAGE || message.version !== 1) return null;
  if (!message.branding || typeof message.branding !== "object") return null;

  const branding = message.branding as Record<string, unknown>;
  const theme = branding.theme;
  if (theme !== "light" && theme !== "dark" && theme !== "obol") return null;
  if (
    typeof branding.displayName !== "string" ||
    branding.displayName.length > 120 ||
    typeof branding.tagline !== "string" ||
    branding.tagline.length > 500 ||
    !branding.themeVars ||
    typeof branding.themeVars !== "object"
  ) {
    return null;
  }

  const themeVars: Record<string, string> = {};
  for (const [key, value] of Object.entries(
    branding.themeVars as Record<string, unknown>,
  )) {
    if (typeof value === "string" && HEX_RE.test(value)) {
      themeVars[key] = value;
    }
  }
  const logoUrl = optionalImage(branding.logoUrl);
  if (logoUrl === undefined) {
    return null;
  }

  return {
    displayName: branding.displayName,
    tagline: branding.tagline,
    theme,
    themeVars,
    logoUrl,
  };
}

export function StorefrontPreview({
  initial,
}: {
  initial: ServiceCatalogDocument;
}) {
  const [storefront, setStorefront] = useState(initial);

  useEffect(() => {
    const origin = operatorOrigin();
    if (!origin || window.parent === window) return;

    const handleMessage = (event: MessageEvent) => {
      if (event.source !== window.parent || event.origin !== origin) return;
      const branding = parsePreviewMessage(event.data);
      if (!branding) return;
      setStorefront((published) => ({
        ...published,
        displayName: branding.displayName,
        tagline: branding.tagline,
        theme: branding.theme,
        themeVars: branding.themeVars,
        logoUrl: branding.logoUrl ?? DEFAULT_LOGO_PATH,
      }));
    };

    window.addEventListener("message", handleMessage);
    const publishedTheme =
      initial.theme === "dark" || initial.theme === "obol"
        ? initial.theme
        : "light";
    window.parent.postMessage(
      {
        type: PREVIEW_READY_MESSAGE,
        version: 1,
        published: {
          displayName: initial.displayName,
          tagline: initial.tagline,
          theme: publishedTheme,
          accent: initial.themeVars?.green ?? "#0b9b71",
          logoUrl: initial.logoUrl,
        },
      },
      origin,
    );
    return () => window.removeEventListener("message", handleMessage);
  }, [initial]);

  useEffect(() => {
    const root = document.documentElement;
    const previewStyle = themeStyle(storefront.themeVars);
    const previous = new Map<string, string>();
    for (const [name, value] of Object.entries(previewStyle)) {
      previous.set(name, root.style.getPropertyValue(name));
      root.style.setProperty(name, value);
    }
    const previousScheme = root.style.colorScheme;
    root.style.colorScheme = isDarkTheme(storefront.theme) ? "dark" : "light";
    return () => {
      for (const [name, value] of previous) {
        if (value) root.style.setProperty(name, value);
        else root.style.removeProperty(name);
      }
      root.style.colorScheme = previousScheme;
    };
  }, [storefront.theme, storefront.themeVars]);

  return (
    <>
      <Header storefront={storefront} />
      <main className="max-w-3xl mx-auto px-4 py-10" data-obol="page-storefront">
        <section className="mb-8" data-obol="hero">
          <h1
            className="text-3xl font-bold text-text-light mb-2"
            data-obol="hero-title"
          >
            {DEFAULT_HERO_TITLE}
          </h1>
          <p className="text-text-body" data-obol="tagline">
            {storefront.tagline}
          </p>
          {storefront.description ? (
            <RichText
              html={storefront.descriptionHtml}
              fallback={storefront.description}
              className="text-text-body text-sm mt-3"
              dataObol="description"
            />
          ) : null}
        </section>

        <ServicesList initial={initial.services} />

        <div className="mt-10 space-y-4">
          <PaymentFlow />
          <nav className="flex gap-4 text-xs">
            <a
              href="/skill.md"
              className="text-obol-green hover:underline font-mono"
            >
              /skill.md
            </a>
            <a
              href="/.well-known/agent-registration.json"
              className="text-obol-green hover:underline font-mono"
            >
              /.well-known/agent-registration.json
            </a>
          </nav>
        </div>
      </main>
    </>
  );
}
