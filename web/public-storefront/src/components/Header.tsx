import Image from "next/image";
import type { StorefrontProfile } from "@/types";
import { isDefaultStorefrontLogo } from "@/lib/catalog";

export function Header({ storefront }: { storefront: StorefrontProfile }) {
  const isDefaultLogo = isDefaultStorefrontLogo(storefront.logoUrl);
  return (
    <header className="border-b border-stroke bg-bg01">
      <div className="max-w-3xl mx-auto px-4 py-4 flex items-center gap-3">
        {isDefaultLogo ? (
          <Image
            src="/obol-stack-logo.png"
            alt={storefront.displayName}
            width={161}
            height={28}
            priority
            className="h-7 w-auto"
          />
        ) : (
          <>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={storefront.logoUrl}
              alt={storefront.displayName}
              className="h-8 w-8 rounded object-cover"
            />
            <div className="min-w-0">
              <div className="text-sm font-semibold text-text-light truncate">
                {storefront.displayName}
              </div>
            </div>
          </>
        )}
      </div>
    </header>
  );
}
