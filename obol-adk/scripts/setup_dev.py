#!/usr/bin/env python3
"""
Development setup script for obol-adk
Clones/updates obol-gitbook and configures .env for local development
"""
import os
import subprocess
import sys
from pathlib import Path


def run_command(cmd, cwd=None, check=True):
    """Run a shell command and return result"""
    try:
        result = subprocess.run(
            cmd,
            shell=True,
            cwd=cwd,
            check=check,
            capture_output=True,
            text=True
        )
        return result.returncode == 0, result.stdout, result.stderr
    except subprocess.CalledProcessError as e:
        return False, e.stdout, e.stderr


def setup_gitbook(base_dir):
    """Clone or update obol-gitbook repository"""
    gitbook_dir = base_dir / "obol-gitbook"

    if gitbook_dir.exists():
        print(f"📚 Updating obol-gitbook...")
        success, stdout, stderr = run_command("git pull", cwd=gitbook_dir)
        if success:
            print(f"✓ obol-gitbook updated")
        else:
            print(f"⚠ Failed to update obol-gitbook: {stderr}")
            return False
    else:
        print(f"📚 Cloning obol-gitbook...")
        success, stdout, stderr = run_command(
            "git clone https://github.com/ObolNetwork/obol-gitbook.git obol-gitbook",
            cwd=base_dir
        )
        if success:
            print(f"✓ obol-gitbook cloned to {gitbook_dir}")
        else:
            print(f"✗ Failed to clone obol-gitbook: {stderr}")
            return False

    return True


def update_env_file(base_dir, gitbook_path):
    """Update .env file with FILESYSTEM_MCP_PATHS"""
    env_file = base_dir / ".env"
    env_example = base_dir / ".env.example"

    # Read existing .env or create from example
    env_lines = []
    if env_file.exists():
        with open(env_file, 'r') as f:
            env_lines = f.readlines()
    elif env_example.exists():
        with open(env_example, 'r') as f:
            env_lines = f.readlines()

    # Check if FILESYSTEM_MCP_PATHS already exists
    has_filesystem_paths = any('FILESYSTEM_MCP_PATHS' in line for line in env_lines)

    if not has_filesystem_paths:
        # Add FILESYSTEM_MCP_PATHS
        if env_lines and not env_lines[-1].endswith('\n'):
            env_lines.append('\n')
        env_lines.append(f'\n# Filesystem MCP Server Paths\n')
        env_lines.append(f'FILESYSTEM_MCP_PATHS={gitbook_path}\n')

        with open(env_file, 'w') as f:
            f.writelines(env_lines)
        print(f"✓ Added FILESYSTEM_MCP_PATHS to {env_file}")
    else:
        print(f"✓ FILESYSTEM_MCP_PATHS already configured in {env_file}")

    # Ensure GOOGLE_API_KEY is present
    has_api_key = any('GOOGLE_API_KEY' in line and not line.strip().startswith('#') for line in env_lines)
    if not has_api_key:
        print(f"⚠ Warning: GOOGLE_API_KEY not found in .env")
        print(f"  Add your API key to {env_file}:")
        print(f"  GOOGLE_API_KEY=your-api-key-here")

    return True


def main():
    """Main setup function"""
    # Get the obol-adk directory (parent of scripts/)
    script_dir = Path(__file__).parent
    base_dir = script_dir.parent

    print("=" * 60)
    print("Obol ADK Development Setup")
    print("=" * 60)
    print()

    # Setup gitbook
    if not setup_gitbook(base_dir):
        sys.exit(1)

    # Update .env
    gitbook_path = str(base_dir / "obol-gitbook")
    if not update_env_file(base_dir, gitbook_path):
        sys.exit(1)

    print()
    print("=" * 60)
    print("✓ Development setup complete!")
    print("=" * 60)
    print()
    print("Next steps:")
    print(f"  1. Ensure GOOGLE_API_KEY is set in {base_dir}/.env")
    print(f"  2. Run: make start")
    print()

    return 0


if __name__ == "__main__":
    sys.exit(main())
