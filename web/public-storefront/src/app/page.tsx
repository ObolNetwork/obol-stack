import { DEFAULT_HERO_TITLE, fetchServices, fetchStorefront } from "@/lib/catalog";
import { Header } from "@/components/Header";
import { ServicesList } from "@/components/ServicesList";
import { PaymentFlow } from "@/components/PaymentFlow";
export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function Home() {
  const [services, storefront] = await Promise.all([
    fetchServices(),
    fetchStorefront(),
  ]);

  return (
    <>
      <Header storefront={storefront} />
      <main className="max-w-3xl mx-auto px-4 py-10">
        <section className="mb-8">
          <h1 className="text-3xl font-bold text-text-light mb-2">
            {DEFAULT_HERO_TITLE}
          </h1>
          <p className="text-text-body">{storefront.tagline}</p>
        </section>

        <ServicesList initial={services} />

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
