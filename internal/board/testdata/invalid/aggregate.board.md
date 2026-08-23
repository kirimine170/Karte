---
type: karte-board
doc_id: board:invalid
title: Invalid Board
version: 1
created: 2026-08-01
updated: 2026-08-01
tags: []
---

# Board

## Cards

### card:a

```yaml
type: resource
title: First
source: ../outside.md
meta:
  title: collision
```

---

### card:a

```yaml
type: alien
title: Second
```

## Edges

```yaml
- id: edge:same
  from: card:a
  to: card:a
  relation: supports
- id: edge:same
  from: card:a
  to: card:a
  relation: supports
- id: edge:missing
  from: card:a
  to: card:missing
  relation: unknown
```

## Layout

```yaml
cards:
  card:a:
    x: .nan
    y: 0
    width: 0
    height: 180
  card:orphan:
    x: 0
    y: 0
    width: 100
    height: 100
viewport:
  x: 0
  y: 0
  zoom: 99
```
