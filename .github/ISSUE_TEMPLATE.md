---
name: Bug Report
about: Report a bug or unexpected behavior
title: "[BUG] "
labels: [bug, triage]
assignees: []

body:
  - type: markdown
    attributes:
      value: |-
        Thanks for taking the time to fill out this bug report!
  - type: input
    id: version
    attributes:
      label: Emdexer Version
      description: What version of Emdexer are you running? (e.g., v1.0, latest Docker image, commit SHA)
      placeholder: v1.0
    validations:
      required: true
  - type: textarea
    id: what-happened
    attributes:
      label: What happened?
      description: Please describe the bug clearly and concisely.
      placeholder: Tell us what you saw!
    validations:
      required: true
  - type: textarea
    id: reproduce
    attributes:
      label: Steps to reproduce
      description: |-
        How can we reproduce the issue?
        Please list the exact steps, including commands run and configurations used.
      placeholder: |
        1. Start Emdexer with 'emdex start'
        2. Configure node with...
        3. Observe error when...
    validations:
      required: true
  - type: textarea
    id: expected-behavior
    attributes:
      label: Expected behavior
      description: What did you expect to happen?
    validations:
      required: true
  - type: textarea
    id: logs
    attributes:
      label: Relevant log output
      description: |-
        Please copy and paste any relevant log output, errors, or stack traces.
        Wrap code blocks with ```golang, ```python, ```bash, etc.
      render: shell
  - type: dropdown
    id: operating-system
    attributes:
      label: Operating System
      options:
        - Linux
        - macOS
        - Windows (WSL)
        - Docker
    validations:
      required: true
  - type: dropdown
    id: architecture
    attributes:
      label: Architecture
      options:
        - amd64
        - arm64
        - armv7
    validations:
      required: true
  - type: checkboxes
    id: terms
    attributes:
      label: Code of Conduct
      description: By submitting this issue, you agree to follow our [Code of Conduct](https://example.com)
      options:
        - label: I agree to follow this project's Code of Conduct
          required: true
