import type { Service } from "@/types";
import { Header } from "@/components/Header";
import { ServicesList } from "@/components/ServicesList";
import { PaymentFlow } from "@/components/PaymentFlow";

// Always render fresh — newly-deployed demos must appear immediately. The
// underlying services.json is built from a Kubernetes ConfigMap that the
// controller updates on every ServiceOffer reconcile, and the client list
// then polls every 10s to surface further changes without a page reload.
export const dynamic = "force-dynamic";
export const revalidate = 0;

async function getServices(): Promise<Service[]> {
  try {
    const res = await fetch(
      `${process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc:8080"}/api/services.json`,
      { cache: "no-store" },
    );
    if (!res.ok) return [];
    return res.json();
  } catch {
    return [];
  }
}

export default async function Home() {
  const services = await getServices();

  return (
    <>
      <Header />
      <main className="max-w-3xl mx-auto px-4 py-10">
        <section className="mb-8">
          <h1 className="text-3xl font-bold text-text-light mb-2">
            Agent services
          </h1>
          <p className="text-text-body">
            This Obol Agent offers the following services for digital payment:
          </p>
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
