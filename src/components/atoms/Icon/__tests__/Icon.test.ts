// Using Node.js assert for testing without Jest dependency
import assert from 'assert';
import fs from 'fs';
import path from 'path';
import type { IconName } from '../Icon';

// Icon Type Safety Validation (Node.js compatible)
export function validateIconTypeSync() {
  // Get all SVG files from public directory
  const publicDir = path.join(process.cwd(), 'public');
  const svgFiles = fs
    .readdirSync(publicDir)
    .filter(file => file.endsWith('.svg'))
    .map(file => file.replace('.svg', ''))
    .sort();

  // Test specific known icons exist
  const requiredIcons = [
    'node-cpu',
    'node-disk',
    'node-memory',
    'node-uptime',
    'peers',
    'stack-icon',
  ];
  requiredIcons.forEach(icon => {
    assert(
      svgFiles.includes(icon),
      `Required icon "${icon}" not found in public directory`
    );
  });

  // These were in the old type but don't exist as files
  const nonExistentIcons = ['ethereum', 'aztec'];
  nonExistentIcons.forEach(icon => {
    assert(
      !svgFiles.includes(icon),
      `Icon "${icon}" should not exist in public directory`
    );
  });

  console.log('✅ Icon type validation passed. Found SVG files:', svgFiles);
  return true;
}

// Runtime validation helper that can be used in development
export function validateIconExists(iconName: IconName): boolean {
  try {
    const publicDir = path.join(process.cwd(), 'public');
    const iconPath = path.join(publicDir, `${iconName}.svg`);
    return fs.existsSync(iconPath);
  } catch {
    return false;
  }
}

// Export the list of available icons for runtime use
export function getAvailableIcons(): string[] {
  try {
    const publicDir = path.join(process.cwd(), 'public');
    return fs
      .readdirSync(publicDir)
      .filter(file => file.endsWith('.svg'))
      .map(file => file.replace('.svg', ''))
      .sort();
  } catch {
    return [];
  }
}
