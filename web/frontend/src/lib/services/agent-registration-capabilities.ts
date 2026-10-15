export interface RegistrationCapabilityService {
  name: string;
  version?: string;
  skills?: string[];
  domains?: string[];
  available?: boolean;
  drainEndsAt?: string;
}

export function isOasfService(service: RegistrationCapabilityService): boolean {
  return service.name.toLowerCase() === 'oasf';
}

export function hasCapabilityTaxonomy(
  service: RegistrationCapabilityService
): boolean {
  return (
    isOasfService(service) &&
    ((service.skills?.length ?? 0) > 0 || (service.domains?.length ?? 0) > 0)
  );
}

export function serviceAvailabilityLabel(
  service: RegistrationCapabilityService
): string | null {
  if (service.drainEndsAt) {
    return `draining until ${service.drainEndsAt}`;
  }
  if (service.available === false) {
    return 'unavailable';
  }
  return null;
}
