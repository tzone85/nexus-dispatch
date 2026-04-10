#!/usr/bin/env python3
"""
MemPalace Python Bridge for NXD.

Thin wrapper that translates NXD's JSON expectations into MemPalace's Python API.
All commands return JSON to stdout: {"status": "ok", ...} or {"status": "error", "message": "..."}.

Usage:
    python3 mempalace_bridge.py health
    python3 mempalace_bridge.py search --query Q --wing W [--room R] [--results N]
    python3 mempalace_bridge.py mine --wing W --room R --text T
    python3 mempalace_bridge.py mine-meta --text T
    python3 mempalace_bridge.py wake-up --wing W
"""

import argparse
import json
import sys

MEMPALACE_MISSING_MSG = "mempalace not installed. Run: pip install mempalace"


def _json_out(data: dict) -> None:
    """Print a JSON object to stdout and exit cleanly."""
    json.dump(data, sys.stdout)
    sys.stdout.write("\n")


def _ok(**fields) -> dict:
    """Build a success envelope."""
    return {"status": "ok", **fields}


def _err(message: str) -> dict:
    """Build an error envelope."""
    return {"status": "error", "message": message}


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def cmd_health(_args: argparse.Namespace) -> dict:
    """Check whether mempalace is importable and return its version."""
    try:
        import mempalace  # noqa: F811
    except ImportError:
        return _err(MEMPALACE_MISSING_MSG)

    version = getattr(mempalace, "__version__", "unknown")
    return _ok(version=version)


def cmd_search(args: argparse.Namespace) -> dict:
    """Semantic search inside the palace."""
    try:
        from mempalace.searcher import search_memories
        from mempalace.config import MempalaceConfig
    except ImportError:
        return _err(MEMPALACE_MISSING_MSG)

    cfg = MempalaceConfig()
    palace_path = cfg.palace_path

    result = search_memories(
        query=args.query,
        palace_path=palace_path,
        wing=args.wing,
        room=args.room,
        n_results=args.results,
    )

    if "error" in result:
        return _err(result["error"])

    return _ok(
        query=result.get("query", args.query),
        filters=result.get("filters", {}),
        results=result.get("results", []),
    )


def cmd_mine(args: argparse.Namespace) -> dict:
    """Store arbitrary text into a specific wing/room."""
    try:
        from mempalace.miner import get_collection, add_drawer, chunk_text
        from mempalace.config import MempalaceConfig
    except ImportError:
        return _err(MEMPALACE_MISSING_MSG)

    cfg = MempalaceConfig()
    palace_path = cfg.palace_path

    text = args.text
    if not text or not text.strip():
        return _err("--text must not be empty")

    try:
        collection = get_collection(palace_path)
    except Exception as exc:
        return _err(f"failed to open palace collection: {exc}")

    source_label = f"nxd://{args.wing}/{args.room}"
    chunks = chunk_text(text, source_label)

    if not chunks:
        # Text was too short to chunk; store as a single drawer.
        chunks = [{"content": text.strip(), "chunk_index": 0}]

    stored = 0
    for chunk in chunks:
        added = add_drawer(
            collection=collection,
            wing=args.wing,
            room=args.room,
            content=chunk["content"],
            source_file=source_label,
            chunk_index=chunk["chunk_index"],
            agent="nxd",
        )
        if added:
            stored += 1

    return _ok(
        wing=args.wing,
        room=args.room,
        chunks_total=len(chunks),
        chunks_stored=stored,
    )


def cmd_mine_meta(args: argparse.Namespace) -> dict:
    """Store text into the shared nxd_meta wing."""
    try:
        from mempalace.miner import get_collection, add_drawer, chunk_text
        from mempalace.config import MempalaceConfig
    except ImportError:
        return _err(MEMPALACE_MISSING_MSG)

    cfg = MempalaceConfig()
    palace_path = cfg.palace_path

    text = args.text
    if not text or not text.strip():
        return _err("--text must not be empty")

    wing = "nxd_meta"
    room = "meta"

    try:
        collection = get_collection(palace_path)
    except Exception as exc:
        return _err(f"failed to open palace collection: {exc}")

    source_label = f"nxd://{wing}/{room}"
    chunks = chunk_text(text, source_label)

    if not chunks:
        chunks = [{"content": text.strip(), "chunk_index": 0}]

    stored = 0
    for chunk in chunks:
        added = add_drawer(
            collection=collection,
            wing=wing,
            room=room,
            content=chunk["content"],
            source_file=source_label,
            chunk_index=chunk["chunk_index"],
            agent="nxd",
        )
        if added:
            stored += 1

    return _ok(
        wing=wing,
        room=room,
        chunks_total=len(chunks),
        chunks_stored=stored,
    )


def cmd_wake_up(args: argparse.Namespace) -> dict:
    """Get L0 + L1 context from the memory stack."""
    try:
        from mempalace.layers import MemoryStack
    except ImportError:
        return _err(MEMPALACE_MISSING_MSG)

    try:
        stack = MemoryStack()
        context = stack.wake_up(wing=args.wing)
    except Exception as exc:
        return _err(f"wake-up failed: {exc}")

    return _ok(
        wing=args.wing,
        context=context,
    )


# ---------------------------------------------------------------------------
# Argument parser
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    """Construct the CLI argument parser."""
    parser = argparse.ArgumentParser(
        prog="mempalace_bridge",
        description="MemPalace JSON bridge for NXD",
    )
    subparsers = parser.add_subparsers(dest="command")

    # health
    sub_health = subparsers.add_parser("health", help="Check mempalace installation")
    sub_health.set_defaults(func=cmd_health)

    # search
    sub_search = subparsers.add_parser("search", help="Semantic search")
    sub_search.add_argument("--query", required=True, help="Search query")
    sub_search.add_argument("--wing", default=None, help="Wing filter")
    sub_search.add_argument("--room", default=None, help="Room filter")
    sub_search.add_argument("--results", type=int, default=5, help="Number of results")
    sub_search.set_defaults(func=cmd_search)

    # mine
    sub_mine = subparsers.add_parser("mine", help="Store text into wing/room")
    sub_mine.add_argument("--wing", required=True, help="Target wing")
    sub_mine.add_argument("--room", required=True, help="Target room")
    sub_mine.add_argument("--text", required=True, help="Text to store")
    sub_mine.set_defaults(func=cmd_mine)

    # mine-meta
    sub_mine_meta = subparsers.add_parser("mine-meta", help="Store text into nxd_meta wing")
    sub_mine_meta.add_argument("--text", required=True, help="Text to store")
    sub_mine_meta.set_defaults(func=cmd_mine_meta)

    # wake-up
    sub_wakeup = subparsers.add_parser("wake-up", help="Get L0+L1 context")
    sub_wakeup.add_argument("--wing", default=None, help="Wing filter for L1")
    sub_wakeup.set_defaults(func=cmd_wake_up)

    return parser


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    """Entry point."""
    parser = build_parser()
    args = parser.parse_args()

    if not args.command:
        _json_out(_err("no command provided. Use: health | search | mine | mine-meta | wake-up"))
        sys.exit(1)

    try:
        result = args.func(args)
    except Exception as exc:
        result = _err(f"unexpected error: {exc}")

    _json_out(result)

    if result.get("status") == "error":
        sys.exit(1)


if __name__ == "__main__":
    main()
