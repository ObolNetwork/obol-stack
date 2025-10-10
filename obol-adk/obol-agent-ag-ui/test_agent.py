#!/usr/bin/env python3
"""
Test script for Obol Agent AG-UI endpoint
"""
import requests
import json
import sys
from typing import Dict, List, Any


class ObolAgentTester:
    def __init__(self, base_url: str = "http://localhost:8000"):
        self.base_url = base_url
        self.session = requests.Session()

    def check_health(self) -> bool:
        """Check if the agent is healthy"""
        try:
            response = self.session.get(f"{self.base_url}/health")
            if response.status_code == 200:
                data = response.json()
                print(f"✓ Health check passed: {data}")
                return True
            else:
                print(f"✗ Health check failed: {response.status_code}")
                return False
        except Exception as e:
            print(f"✗ Health check error: {e}")
            return False

    def send_message(self, message: str, thread_id: str = "test-thread") -> Dict[str, Any]:
        """Send a message to the agent and collect streaming response"""
        payload = {
            "threadId": thread_id,
            "runId": f"run-{thread_id}",
            "tools": [],
            "context": [],
            "forwardedProps": {"config": {}, "threadMetadata": {}},
            "state": {},
            "messages": [
                {
                    "id": "msg-1",
                    "role": "user",
                    "content": message
                }
            ]
        }

        try:
            response = self.session.post(
                f"{self.base_url}/",
                json=payload,
                stream=True,
                headers={"Content-Type": "application/json"}
            )

            if response.status_code != 200:
                return {
                    "success": False,
                    "error": f"HTTP {response.status_code}",
                    "response": None
                }

            # Collect all streaming events
            events = []
            full_text = ""

            for line in response.iter_lines():
                if line:
                    line_str = line.decode('utf-8')
                    if line_str.startswith('data: '):
                        event_data = json.loads(line_str[6:])
                        events.append(event_data)

                        # Collect text content
                        if event_data.get('type') == 'TEXT_MESSAGE_CONTENT':
                            full_text += event_data.get('delta', '')

            return {
                "success": True,
                "events": events,
                "full_text": full_text.strip()
            }

        except Exception as e:
            return {
                "success": False,
                "error": str(e),
                "response": None
            }

    def run_tests(self):
        """Run a suite of tests"""
        print("=" * 60)
        print("Obol Agent AG-UI Test Suite")
        print("=" * 60)
        print()

        # Test 1: Health check
        print("Test 1: Health Check")
        print("-" * 60)
        if not self.check_health():
            print("✗ Cannot proceed with tests - agent is not healthy")
            return False
        print()

        # Test 2: List available tools
        print("Test 2: List Available Tools")
        print("-" * 60)
        result = self.send_message("What tools do you have access to?")
        if result['success']:
            print(f"✓ Response received ({len(result['events'])} events)")
            print(f"Full text preview: {result['full_text'][:200]}...")

            # Check for expected tools
            text = result['full_text'].lower()
            expected_tools = ['obol', 'filesystem', 'kubectl']
            found_tools = [tool for tool in expected_tools if tool in text]
            print(f"✓ Found tools: {', '.join(found_tools)}")
        else:
            print(f"✗ Failed: {result.get('error')}")
        print()

        # Test 3: Query Obol cluster information
        print("Test 3: Query Obol API")
        print("-" * 60)
        result = self.send_message("Can you list the available Obol API functions?")
        if result['success']:
            print(f"✓ Response received ({len(result['events'])} events)")
            print(f"Preview: {result['full_text'][:300]}...")
        else:
            print(f"✗ Failed: {result.get('error')}")
        print()

        # Test 4: Documentation access (filesystem)
        print("Test 4: Documentation Access")
        print("-" * 60)
        result = self.send_message("Search for documentation about 'quickstart' or 'getting started'")
        if result['success']:
            print(f"✓ Response received ({len(result['events'])} events)")
            print(f"Preview: {result['full_text'][:300]}...")
        else:
            print(f"✗ Failed: {result.get('error')}")
        print()

        print("=" * 60)
        print("Test Suite Complete")
        print("=" * 60)
        return True


def main():
    # Check if custom URL provided
    base_url = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8000"

    tester = ObolAgentTester(base_url)
    success = tester.run_tests()

    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
