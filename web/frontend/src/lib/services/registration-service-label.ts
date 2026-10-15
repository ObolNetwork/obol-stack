/** Label for a row in /.well-known/agent-registration.json services[]. */
export function registrationServiceLabel(
  service: { name?: string; endpoint?: string },
  index: number
): string {
  const endpoint = service.endpoint?.trim();
  if (endpoint) {
    try {
      const path = new URL(endpoint).pathname;
      const segment = path.split('/').filter(Boolean).pop();
      if (segment) return segment;
    } catch {
      const segment = endpoint.split('/').filter(Boolean).pop();
      if (segment) return segment;
    }
  }
  const name = service.name?.trim();
  if (name && name !== 'web') return name;
  return name ? `${name} #${index + 1}` : `service-${index + 1}`;
}
