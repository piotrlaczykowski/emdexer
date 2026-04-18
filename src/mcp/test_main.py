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


class SearchModeTests(unittest.TestCase):
    def _mock_response(self, results=None):
        m = MagicMock()
        m.json.return_value = {"results": results or [], "mode": "any"}
        m.raise_for_status.return_value = None
        return m

    @patch("main.requests.get")
    def test_search_semantic_sends_mode_semantic(self, mock_get):
        mock_get.return_value = self._mock_response()
        main.search_semantic("hello", "default", ctx=None)
        mock_get.assert_called_once()
        _, kwargs = mock_get.call_args
        assert kwargs["params"]["mode"] == "semantic", kwargs["params"]
        assert kwargs["params"]["q"] == "hello"
        assert kwargs["params"]["namespace"] == "default"

    @patch("main.requests.get")
    def test_search_keyword_sends_mode_keyword(self, mock_get):
        mock_get.return_value = self._mock_response()
        main.search_keyword("foo_bar", "default", ctx=None)
        mock_get.assert_called_once()
        _, kwargs = mock_get.call_args
        assert kwargs["params"]["mode"] == "keyword", kwargs["params"]

    @patch("main.requests.get")
    def test_search_hybrid_sends_mode_hybrid(self, mock_get):
        mock_get.return_value = self._mock_response()
        main.search_hybrid("hello", "default", ctx=None)
        mock_get.assert_called_once()
        _, kwargs = mock_get.call_args
        assert kwargs["params"]["mode"] == "hybrid", kwargs["params"]

    @patch("main.search_hybrid")
    def test_search_files_aliases_search_hybrid(self, mock_hybrid):
        mock_hybrid.return_value = "ok"
        out = main.search_files("hello", "default", ctx=None)
        mock_hybrid.assert_called_once_with("hello", "default", None)
        assert out == "ok"
