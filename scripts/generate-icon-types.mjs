#!/usr/bin/env node

import fs from 'fs';
import path from 'path';

/**
 * Script to generate IconName type from actual SVG files in /public
 * Run with: node scripts/generate-icon-types.js
 */

const PUBLIC_DIR = path.join(__dirname, '..', 'public');
const ICON_COMPONENT_PATH = path.join(
  __dirname,
  '..',
  'src',
  'components',
  'atoms',
  'Icon',
  'Icon.tsx'
);

function getSvgFiles() {
  try {
    const files = fs.readdirSync(PUBLIC_DIR);
    return files
      .filter(file => file.endsWith('.svg'))
      .map(file => file.replace('.svg', ''))
      .sort();
  } catch (error) {
    console.error('Error reading public directory:', error);
    process.exit(1);
  }
}

function generateIconNameType(iconNames) {
  const iconUnion = iconNames.map(name => `  | '${name}'`).join('\n');

  return `// AUTO-GENERATED: Do not edit manually
// Generated from SVG files in /public directory
// Run 'npm run generate-icon-types' to regenerate
export type IconName =
${iconUnion};`;
}

function updateIconComponent(newTypeDefinition) {
  try {
    let content = fs.readFileSync(ICON_COMPONENT_PATH, 'utf8');

    // Find and replace the IconName type definition
    const typeRegex =
      /\/\/ (?:AUTO-GENERATED: Do not edit manually[\s\S]*?)?(?:\/\/ Define available icon names[\s\S]*?)?(?:\/\/ TODO:[\s\S]*?)?export type IconName =[\s\S]*?;/;

    if (typeRegex.test(content)) {
      content = content.replace(typeRegex, newTypeDefinition);
    } else {
      console.error('Could not find IconName type definition to replace');
      process.exit(1);
    }

    fs.writeFileSync(ICON_COMPONENT_PATH, content, 'utf8');
    console.log('✅ IconName type updated successfully');
  } catch (error) {
    console.error('Error updating Icon component:', error);
    process.exit(1);
  }
}

function main() {
  console.log('🔍 Scanning for SVG files in /public...');

  const svgFiles = getSvgFiles();

  if (svgFiles.length === 0) {
    console.warn('⚠️  No SVG files found in /public directory');
    return;
  }

  console.log(`📁 Found ${svgFiles.length} SVG files:`, svgFiles);

  const newTypeDefinition = generateIconNameType(svgFiles);

  console.log('🔄 Updating IconName type...');
  updateIconComponent(newTypeDefinition);

  console.log('✨ Type generation complete!');
  console.log('\nGenerated IconName type:');
  console.log(newTypeDefinition);
}

// Run main function if this file is executed directly
main();
