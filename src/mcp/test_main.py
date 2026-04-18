"""Tests for src/mcp/main.py — covers _render_results helper and the search tools.

Run from src/mcp/:
    python -m unittest test_main -v
"""
import os
import sys
import unittest
from unittest.mock import patch, MagicMock

# EMDEX_AUTH_KEY must be set BEFORE importing main (module raises at import otherwise).
os.environ.setdefault("EMDEX_AUTH_KEY", "test-key")

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import main  # noqa: E402


class RenderResultsTests(unittest.TestCase):
    def test_render_results_empty_returns_no_results_message(self):
        out = main._render_results([], title="Search results", namespace="default", ctx=None)
        assert isinstance(out, str)
        assert "No results found" in out
        assert "default" in out

    def test_render_results_populated_returns_markdown_table(self):
        results = [
            {"score": 0.42, "payload": {"path": "src/foo.py", "text": "hello world"}},
            {"score": 0.21, "payload": {"path": "src/bar.py", "text": "goodbye"}},
        ]
        out = main._render_results(results, title="Search results", namespace="default", ctx=None)
        assert isinstance(out, str)
        assert "src/foo.py" in out
        assert "src/bar.py" in out
        assert "0.42" in out
        assert "| # | Path |" in out
