# ADR-0002: Pre-moderation model for Youth Stories

## Status
Accepted

## Context
The Youth Stories feature allows users to submit narrative posts about their travel experiences. The platform needs a moderation workflow to ensure content quality and safety before stories are visible to the broader community.

Two moderation models were considered:
1. **Pre-moderation** (chosen): Stories are created with `status = 'pending'` and are only visible on the Youth feed after an admin approves them (`status = 'approved'`).
2. **Post-moderation**: Stories are visible immediately upon creation and removed by moderators if they violate guidelines.

## Decision
We use a **pre-moderation model** where:
- All new stories are inserted with `status = 'pending'`
- The Youth feed (`/v1/public/stories`) filters on `WHERE status = 'approved'`
- The author's My Stories view shows all their stories regardless of status, with a status tag (pending/approved/rejected)
- Admin moderators review pending stories and approve or reject them via `/v1/stories/{id}/moderation`
- Rejected stories are visible only to the author with a rejection indicator

## Consequences
- **Positive**: No harmful content reaches the public feed before review
- **Positive**: Authors can see their own pending stories and understand the moderation state
- **Negative**: There is a delay between story creation and public visibility
- **Negative**: Moderators must review every story before it appears on the Youth feed
- **Trade-off**: This is acceptable for a youth-focused platform where content safety is paramount
