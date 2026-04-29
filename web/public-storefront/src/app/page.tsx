import type { Service } from "@/types";
import { ServiceCard } from "@/components/ServiceCard";
import { PaymentFlow } from "@/components/PaymentFlow";

async function getServices(): Promise<Service[]> {
  try {
    const res = await fetch(
      `${process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc:8080"}/api/services.json`,
      { next: { revalidate: 30 } },
    );
    if (!res.ok) return [];
    return res.json();
  } catch {
    return [];
  }
}

export default async function Home() {
  const services = await getServices();
  const demos = services.filter((s) => s.isDemo);
  const others = services.filter((s) => !s.isDemo);

  return (
    <main className="max-w-3xl mx-auto px-4 py-10">
      <header className="mb-8">
        <h1 className="text-3xl font-bold text-obol-text mb-2">Obol Stack</h1>
        <p className="text-obol-muted">
          This node sells decentralised infrastructure services via{" "}
          <a
            href="https://www.x402.org"
            className="text-obol-green hover:underline"
            target="_blank"
            rel="noopener noreferrer"
          >
            x402
          </a>{" "}
          micropayments.
        </p>
      </header>

      {services.length === 0 ? (
        <div className="rounded-lg border border-obol-border bg-obol-bg-card p-8 text-center">
          <p className="text-obol-muted">
            No services are currently available.
          </p>
          <p className="text-sm text-obol-muted mt-1">
            Run{" "}
            <code className="text-obol-green font-mono">
              obol sell demo hello
            </code>{" "}
            to deploy your first demo service.
          </p>
        </div>
      ) : (
        <div className="space-y-8">
          {demos.length > 0 && (
            <section>
              <h2 className="text-lg font-semibold text-obol-text mb-3">
                Demo Services
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
              <h2 className="text-lg font-semibold text-obol-text mb-3">
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
      )}

      <div className="mt-10 space-y-4">
        <PaymentFlow />

        <nav className="flex gap-4 text-sm">
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
  );
}
