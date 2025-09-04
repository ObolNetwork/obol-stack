/**
 * Random walk utility functions for consistent behavior across components
 * Provides bias-free random walk with proper mean reversion and strict bounds enforcement
 */

// Box-Muller transform to generate normally distributed random numbers
export function generateNormalRandom(
  mean: number,
  standardDeviation: number
): number {
  let u = 0,
    v = 0;
  while (u === 0) u = Math.random(); // Converting [0,1) to (0,1)
  while (v === 0) v = Math.random();

  const normal = Math.sqrt(-2.0 * Math.log(u)) * Math.cos(2.0 * Math.PI * v);
  return mean + standardDeviation * normal; // Don't round here to avoid bias
}

export interface RandomWalkConfig {
  target: number;
  standardDeviation: number;
  minValue?: number;
  maxValue?: number;
  precision?: number; // Number of decimal places (0 for integers)
  meanReversionStrength?: number; // 0-1, higher = stronger pull to target
  varianceScale?: number; // Scale factor for random step size
}

/**
 * Performs one step of a random walk with mean reversion
 *
 * IMPORTANT: Applies bounds AFTER rounding to prevent values from exceeding
 * hard limits due to precision rounding (e.g., 99.96 rounding to 100.1 when maxValue is 100.0)
 *
 * @param currentValue Current value
 * @param config Random walk configuration
 * @returns New value after one step, guaranteed to be within [minValue, maxValue]
 */
export function randomWalkStep(
  currentValue: number,
  config: RandomWalkConfig
): number {
  const {
    target,
    standardDeviation,
    minValue = target - 3 * standardDeviation,
    maxValue = target + 3 * standardDeviation,
    precision = 0,
    meanReversionStrength = 0.25,
    varianceScale = 0.15,
  } = config;

  // Generate random step with proper variance scaling
  const randomStep = generateNormalRandom(0, standardDeviation * varianceScale);

  // Mean reversion to prevent drift
  const meanReversion = (target - currentValue) * meanReversionStrength;

  let newValue = currentValue + randomStep + meanReversion;

  // Round to specified precision first
  const multiplier = Math.pow(10, precision);
  newValue = Math.round(newValue * multiplier) / multiplier;

  // Apply bounds after rounding to ensure hard limits are respected
  return Math.max(minValue, Math.min(maxValue, newValue));
}
