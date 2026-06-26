/** Format node uptime hours as "2d 4h", "12h", or "0h". */
export function formatUptime(hours: number): string {
  if (hours <= 0) return '0h';
  const days = Math.floor(hours / 24);
  const remainingHours = hours % 24;
  if (days > 0) return `${days}d ${remainingHours}h`;
  return `${remainingHours}h`;
}
