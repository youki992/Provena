# Skills Guide

[中文](../zh-CN/skills-guide.md)

Skills give the Agent topic capabilities, procedure descriptions, templates, and reference material that can be loaded on demand. They are suited to carrying stable methodology, not one-off task input.

## Directory Structure

Default directory:

```yaml
skills_dir: skills
```

Recommended structure:

```text
skills/
  api-security-testing/
    SKILL.md
  ssrf-testing/
    SKILL.md
  provena-eino-demo/
    SKILL.md
    REFERENCE.md
    assets/
```

Every Skill contains at least `SKILL.md`.

## SKILL.md

`SKILL.md` uses YAML front matter:

```markdown
---
name: ssrf-testing
description: SSRF vulnerability identification, validation, bypass, and remediation workflow
---

# SSRF Testing

Use this skill when the task involves server-side request forgery, URL callbacks, cloud metadata access, or internal network probing.
```

`description` is important; the Agent uses it to decide when to load the skill.

## Progressive Disclosure

Eino Skills support on-demand loading. Configuration:

```yaml
multi_agent:
  eino_skills:
    disable: false
    filesystem_tools: true
    skill_tool_name: skill
```

At first the Agent only sees the skill name and description, and calls `skill` to read the details only when it is really needed, reducing context usage.

## What Is Suitable to Write as a Skill

- The testing procedure for a certain class of vulnerability.
- A security audit checklist.
- Report templates.
- Methods for combining tools.
- Internal standards.
- Judging common false positives.

Not suitable:

- Temporary target information.
- API keys, passwords, cookies.
- Frequently changing scan results.
- Large volumes of unstructured raw logs.

## Attached Files

A Skill can carry attached files, such as `REFERENCE.md`, templates, dictionaries, or examples. `SKILL.md` should state when to read these files.

Recommendations:

- Keep the main file short and clear.
- Split reference material by topic.
- Read large files only when necessary.

## Binding to Roles

A role can prompt the Agent to use a certain kind of Skill; a Skill can also be bound to a role. Recommendations:

- Keep general-purpose skills unbound, so they trigger automatically based on their description.
- Bind high-risk skills to dedicated roles.
- Do not let the descriptions of similar skills overlap too much.

## Development Recommendations

Skill content structure:

1. Trigger scenarios.
2. Goals and boundaries.
3. Operating steps.
4. Tool recommendations.
5. Output format.
6. Risks and prohibitions.
7. Reference material.

Write it so the Agent can execute it, not just so a person can read it.

## Troubleshooting

A Skill is not used:

- The `description` is too narrow or too vague.
- The task does not contain the trigger keywords.
- `multi_agent.eino_skills.disable: true`.
- The Skill file's front matter format is wrong.

A Skill reads too much:

- Split the attached files.
- In `SKILL.md`, state clearly: "only read Y when you need X".
- Remove duplicate content.

## Deep Water of Skill Design

The core value of a Skill is not "letting the Agent know a concept", but giving the Agent an executable procedure at the right moment. When writing a Skill, pay special attention to the trigger conditions and the exit conditions.

Recommended structure:

```markdown
## When to use
State the trigger scenarios clearly.

## Preconditions
What the user must provide and what the target must satisfy.

## Procedure
Execute step by step; each step explains the tools, inputs, and judging criteria.

## Stop conditions
Under what circumstances to stop, escalate for approval, or hand over to a human.

## Output
The format of the final result.
```

## Anti-Patterns

| Anti-pattern | Consequence | Fix |
| --- | --- | --- |
| Description too broad: `used for security testing` | Almost every task triggers it | Write specific vulnerabilities, scenarios, and signals |
| Content reads like an encyclopedia | The Agent does not know what to do next | Rewrite it as procedures and decision trees |
| Writing sensitive configuration into a Skill | Leakage and misuse | Use runtime configuration or user input |
| One Skill holds everything | High read cost, chaotic recall | Split by vulnerability/task |
| No stop condition | The Agent may keep expanding scope | State clearly when to stop and approve |

## Difference Between a Skill and the Knowledge Base

- Skill: guides the Agent on how to do something, emphasizing procedure.
- Knowledge base: provides facts, cases, and references, emphasizing retrieval.

For example, SSRF:

- The Skill writes "how to test SSRF, how to judge it, when to stop".
- The knowledge base writes "cloud provider metadata addresses, historical bypasses, remediation plans".

## Local File Tool Risks

`filesystem_tools: true` exposes read, write, and execute capabilities. It is very useful for development and automation, but it is also a security boundary. For production it is recommended to:

- Use `workspace_root_dir` to confine the working directory.
- Use HITL for write and execute actions.
- Do not add `execute` to the global allowlist.
- Make the Skill explicitly forbid reading and writing files outside the authorized scope.

## Source Anchors

- Skill package validation: `internal/skillpackage/validate.go`
- Skill service: `internal/skillpackage/service.go`
- Eino Skills integration: `internal/multiagent/eino_skills.go`
- Skills Handler: `internal/handler/skills.go`
