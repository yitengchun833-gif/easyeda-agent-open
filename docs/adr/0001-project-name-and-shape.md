# ADR 0001: Project Name and Repository Shape

## Status

Accepted.

## Decision

Use `easyeda-agent` as the project name.

Use one repository for the initial phase:

- Go CLI/daemon
- protocol definitions
- EasyEDA connector notes and future source
- schematic Skill draft
- architecture and roadmap documents

## Context

The project is not only a bridge server. It is an AI-native automation layer around EasyEDA, where Go owns the reliable tool surface and Skills own expert workflow.

Names like `easyeda-bridge` and `easyeda-gateway` are too narrow because they describe transport only. `easyeda-agent` leaves room for CLI, daemon, connector, Skills, artifacts, and verification loops.

## Consequences

- Package and command names can stay short: `easyeda`.
- Future repos may split out the EasyEDA extension or Skills if release cadence demands it.
- The first implementation can move quickly without cross-repo coordination.
