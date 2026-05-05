"""Validate the OpenClaw SKILL.md contract.

The SKILL.md frontmatter is the OpenClaw discovery trigger. Breaking
its shape silently disables the skill for users, so this test guards:
  - File exists at the canonical path
  - Frontmatter parses as YAML
  - Only `name` and `description` keys are present (skill-creator spec)
  - `description` is non-empty and mentions `GATEWAY_URL` and `EMDEX_AUTH_KEY`
  - Body length is under the 500-line cap
"""
from __future__ import annotations

import re
from pathlib import Path

import pytest
import yaml

SKILL_PATH = Path(__file__).parent / "openclaw-skill" / "SKILL.md"
ALLOWED_KEYS = {"name", "description"}


def _load_skill():
    assert SKILL_PATH.exists(), f"missing {SKILL_PATH}"
    text = SKILL_PATH.read_text()
    m = re.match(r"^---\n(.*?)\n---\n(.*)$", text, re.DOTALL)
    assert m, "SKILL.md must start with YAML frontmatter delimited by ---"
    fm = yaml.safe_load(m.group(1)) or {}
    body = m.group(2)
    return fm, body


def test_frontmatter_keys_are_only_name_and_description():
    fm, _ = _load_skill()
    assert set(fm.keys()) == ALLOWED_KEYS, f"unexpected keys: {set(fm.keys()) - ALLOWED_KEYS}"


def test_name_is_emdexer():
    fm, _ = _load_skill()
    assert fm["name"] == "emdexer", f"got {fm['name']!r}"


def test_description_mentions_required_env_vars():
    fm, _ = _load_skill()
    desc = fm["description"]
    assert "GATEWAY_URL" in desc, "description is missing GATEWAY_URL"
    assert "EMDEX_AUTH_KEY" in desc, "description is missing EMDEX_AUTH_KEY"
    assert len(desc) >= 100, "description must be comprehensive (OpenClaw uses it as the trigger)"


def test_body_under_500_lines():
    _, body = _load_skill()
    line_count = body.count("\n")
    assert line_count < 500, f"SKILL.md body has {line_count} lines, must stay under 500"
