import { isOasfService } from './agent-registration-capabilities.ts';
import { registrationServiceLabel } from './registration-service-label.ts';

export interface RegistrationPublishedService {
  name: string;
  endpoint: string;
  skills: string[];
  version?: string;
}

/** Pair each `web` endpoint with the following OASF skills block in the doc. */
export function listPublishedRegistrationServices(
  services: ReadonlyArray<{
    name?: string;
    endpoint?: string;
    version?: string;
    skills?: string[];
  }>
): RegistrationPublishedService[] {
  const out: RegistrationPublishedService[] = [];

  for (let index = 0; index < services.length; index++) {
    const service = services[index];
    const endpoint = service.endpoint?.trim();
    if (!endpoint) continue;

    const next = services[index + 1];
    const oasf = next && isOasfService({ name: next.name ?? '' }) ? next : null;

    out.push({
      name: registrationServiceLabel(service, out.length),
      endpoint,
      skills: oasf?.skills ?? [],
      version: oasf?.version,
    });
  }

  return out;
}
