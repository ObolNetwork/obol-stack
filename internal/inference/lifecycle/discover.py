"""Discovery module — finds new GGUF models via x-cli social signal scraping."""

import json
import logging
import os
import re
import subprocess
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Dict, List, Optional

logger = logging.getLogger(__name__)

# x-cli binary expected on PATH (includes ~/.local/bin)
X_CLI_BIN = "x-cli"
HERMES_ENV = Path.home() / ".hermes" / ".env"

# Known quant patterns
QUANT_PATTERNS = re.compile(
    r"(Q[2-8]_[KMS](?:_[SML])?|IQ[1-4]_[A-Z]+|F16|F32|BF16)", re.IGNORECASE
)
# HuggingFace URL pattern
HF_URL_PATTERN = re.compile(
    r"(?:https?://)?huggingface\.co/([a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+)"
)
# Model size from filename like 7B, 13B, 70B
SIZE_PATTERN = re.compile(r"(\d+(?:\.\d+)?)\s*[Bb](?:illion)?")


@dataclass
class DiscoveryCandidate:
    """A model candidate discovered from social signals."""
    name: str
    hf_url: Optional[str]
    quant: Optional[str]
    size_gb_est: Optional[float]
    signal_score: float
    source_tweet_id: str
    discovered_at: str


def _load_hermes_env() -> Dict[str, str]:
    """Load environment variables from ~/.hermes/.env for x-cli auth."""
    env = os.environ.copy()
    env["PATH"] = f"{Path.home() / '.local' / 'bin'}:{env.get('PATH', '')}"
    if HERMES_ENV.exists():
        try:
            with open(HERMES_ENV) as f:
                for line in f:
                    line = line.strip()
                    if line and not line.startswith("#") and "=" in line:
                        key, _, value = line.partition("=")
                        env[key.strip()] = value.strip().strip("\"'")
            logger.debug("Loaded %s env vars from %s", len(env), HERMES_ENV)
        except IOError as e:
            logger.warning("Could not read hermes env: %s", e)
    return env


def search_localllama(max_results: int = 20) -> str:
    """Search x/LocalLLaMA community for GGUF/model/benchmark tweets.

    Uses x-cli to search recent posts from the LocalLLaMA community
    for model announcements, GGUF releases, and benchmark discussions.

    Returns:
        Raw JSON string from x-cli search results.
    """
    env = _load_hermes_env()
    queries = [
        "LocalLLaMA GGUF new model",
        "LocalLLaMA benchmark release quantized",
        "GGUF huggingface release llama",
    ]
    all_results = []
    for query in queries:
        try:
            result = subprocess.run(
                [X_CLI_BIN, "search", "--query", query,
                 "--max-results", str(max_results // len(queries)),
                 "--format", "json"],
                capture_output=True, text=True, timeout=30, env=env
            )
            if result.returncode == 0 and result.stdout.strip():
                try:
                    tweets = json.loads(result.stdout)
                    if isinstance(tweets, list):
                        all_results.extend(tweets)
                    elif isinstance(tweets, dict) and "data" in tweets:
                        all_results.extend(tweets["data"])
                except json.JSONDecodeError:
                    logger.warning("Failed to parse x-cli output for query: %s", query)
            else:
                logger.warning("x-cli search failed for '%s': %s", query, result.stderr[:200])
        except (subprocess.TimeoutExpired, FileNotFoundError) as e:
            logger.error("x-cli execution error: %s", e)

    logger.info("Discovered %d raw tweets from x-cli search", len(all_results))
    return json.dumps(all_results)


def parse_candidates(tweets_json: str) -> List[DiscoveryCandidate]:
    """Extract model candidates from tweet JSON data.

    Parses tweet text for HuggingFace URLs, quant types, model sizes,
    and model names. Deduplicates by HF URL.

    Args:
        tweets_json: JSON string of tweet objects.

    Returns:
        List of DiscoveryCandidate objects.
    """
    try:
        tweets = json.loads(tweets_json)
    except json.JSONDecodeError:
        logger.error("Invalid JSON in tweets data")
        return []

    if not isinstance(tweets, list):
        tweets = [tweets]

    candidates = []
    seen_urls = set()

    for tweet in tweets:
        text = tweet.get("text", "") or tweet.get("content", "")
        tweet_id = str(tweet.get("id", tweet.get("tweet_id", "unknown")))

        # Extract HF URL
        hf_match = HF_URL_PATTERN.search(text)
        hf_url = f"https://huggingface.co/{hf_match.group(1)}" if hf_match else None

        if hf_url and hf_url in seen_urls:
            continue
        if hf_url:
            seen_urls.add(hf_url)

        # Extract quant
        quant_match = QUANT_PATTERNS.search(text)
        quant = quant_match.group(1).upper() if quant_match else None

        # Extract model size estimate
        size_match = SIZE_PATTERN.search(text)
        size_gb_est = None
        if size_match:
            param_b = float(size_match.group(1))
            # Rough estimate: Q4 ~ 0.5 GB/B params, Q8 ~ 1 GB/B
            multiplier = 0.5 if quant and "4" in quant else 0.75
            size_gb_est = round(param_b * multiplier, 1)

        # Extract model name (first capitalized multi-word near "model" or from HF URL)
        name = "Unknown Model"
        if hf_match:
            name = hf_match.group(1).split("/")[-1]
        else:
            name_match = re.search(r"([A-Z][a-zA-Z0-9]*(?:[-_][A-Za-z0-9]+){1,5})", text)
            if name_match:
                name = name_match.group(1)

        signal = score_signal(tweet)

        candidates.append(DiscoveryCandidate(
            name=name,
            hf_url=hf_url,
            quant=quant,
            size_gb_est=size_gb_est,
            signal_score=signal,
            source_tweet_id=tweet_id,
            discovered_at=datetime.utcnow().isoformat(),
        ))

    # Sort by signal score descending
    candidates.sort(key=lambda c: c.signal_score, reverse=True)
    logger.info("Parsed %d candidates from %d tweets", len(candidates), len(tweets))
    return candidates


def score_signal(tweet: dict) -> float:
    """Compute a signal score for a tweet based on engagement metrics.

    Formula: likes * 1.0 + bookmarks * 2.0 + retweets * 1.5

    Args:
        tweet: Tweet dict with engagement metric fields.

    Returns:
        Weighted engagement score.
    """
    likes = float(tweet.get("likes", 0) or tweet.get("like_count", 0) or 0)
    bookmarks = float(tweet.get("bookmarks", 0) or tweet.get("bookmark_count", 0) or 0)
    retweets = float(tweet.get("retweets", 0) or tweet.get("retweet_count", 0) or 0)
    return likes * 1.0 + bookmarks * 2.0 + retweets * 1.5


def filter_candidates(candidates: List[DiscoveryCandidate],
                       max_size_gb: float) -> List[DiscoveryCandidate]:
    """Filter candidates that exceed the hardware's capacity.

    Removes models with estimated size larger than max_size_gb.
    Models with unknown size are kept (benefit of the doubt).

    Args:
        candidates: List of discovery candidates.
        max_size_gb: Maximum model size in GB.

    Returns:
        Filtered list of candidates.
    """
    filtered = [
        c for c in candidates
        if c.size_gb_est is None or c.size_gb_est <= max_size_gb
    ]
    removed = len(candidates) - len(filtered)
    if removed:
        logger.info("Filtered out %d candidates exceeding %.1f GB", removed, max_size_gb)
    return filtered
