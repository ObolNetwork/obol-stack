/**
 * Feature Flags Configuration
 *
 * Feature flags allow you to enable/disable features without deploying new code.
 * Flags can be controlled via environment variables with the prefix NEXT_PUBLIC_FEATURE_
 *
 * Usage:
 *   - Set NEXT_PUBLIC_FEATURE_EXAMPLE=true to enable a flag
 *   - Set NEXT_PUBLIC_FEATURE_EXAMPLE=false or leave unset to disable
 *   - Use isFeatureEnabled() or access featureFlags directly: const isEnabled = isFeatureEnabled('example')
 */

/**
 * Define all available feature flags here.
 * The key is the flag name (lowercase), and the value is the default state.
 */
export const featureFlags = {
  showTagVersionAndShaCommit:
    process.env.NEXT_PUBLIC_FEATURE_TAG_VERSION_SHA_COMMIT === 'true',
} as const;

/**
 * Type for feature flag names
 */
export type FeatureFlagName = keyof typeof featureFlags;

/**
 * Check if a feature flag is enabled
 *
 * @param flagName - The name of the feature flag
 * @returns True if the flag is enabled, false otherwise
 */
export function isFeatureEnabled(flagName: FeatureFlagName): boolean {
  return featureFlags[flagName];
}
