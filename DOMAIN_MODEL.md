# Domain Model

This is a **conceptual sketch** — entities and how they relate — not a database schema. Exact fields, types, and constraints are owned by the tasks that actually build each area (Task 3 for identity/organizations, Task 4 for course content, Task 5 for learner progress/certificates, Task 6 for commerce) and may reasonably differ in detail from this sketch. Do not treat this document as a binding schema.

## Core entities

- **Organization** — a customer's workspace/tenant on the platform. Has many Memberships (linking Users with a role) and Courses.
- **User** — a person account, authenticated via Supabase Auth. May be a platform owner (a global flag/role, not tied to a specific organization) and/or hold a Membership (with a role) in one or more organizations.
- **Membership** — links a User to an Organization with a role: organization owner, teacher/creator, moderator, or learner. (Platform owner is a separate, global distinction — see User above.)
- **Course** — belongs to an Organization. Has many Chapters, Offers, and Purchases.
- **Chapter** — groups Lessons within a Course, in order.
- **Lesson** — belongs to a Chapter. Has many Blocks, and one Progress record per learner.
- **Block** — a single piece of content within a Lesson (text, image, video, file, or quiz). Quiz blocks store their questions/answers server-side only.
- **Offer** — a pricing variant of a Course (free or paid). Has many Purchases.
- **Purchase** — a learner's enrollment/payment record against a Course via an Offer. Has one Entitlement.
- **Entitlement** — derived from a Purchase; the actual access grant used to decide whether a learner may view a Course.
- **Progress** — tracks a learner's completion status and quiz results per Lesson.
- **Certificate** — issued to a learner upon Course completion; carries a public verification identifier.
- **Notification** — a record of an email sent to a User (course assigned, lesson published, certificate earned, etc.), optionally tied to a Course.

## Relationships

```
Organization
  |-- has many Users (through Membership)
  |-- has many Courses
  |-- has many Memberships

User
  |-- has many Memberships
  |-- may be platform owner (global, independent of any Membership)
  |-- has many Progress records (as learner)
  |-- has many Certificates (as learner)
  |-- has many Courses (as teacher/creator, via Membership role)
  |-- has many Notifications

Course
  |-- belongs to Organization
  |-- has many Chapters
  |-- has many Offers
  |-- has many Purchases
  |-- has many Entitlements
  |-- has many Certificates

Chapter
  |-- belongs to Course
  |-- has many Lessons

Lesson
  |-- belongs to Chapter
  |-- has many Blocks
  |-- has many Progress (one per learner)

Block
  |-- belongs to Lesson

Offer
  |-- belongs to Course
  |-- has many Purchases

Purchase
  |-- belongs to Course
  |-- belongs to User (learner)
  |-- belongs to Offer
  |-- has one Entitlement

Entitlement
  |-- belongs to Purchase
  |-- belongs to Course
  |-- belongs to User (learner)

Progress
  |-- belongs to Lesson
  |-- belongs to User (learner)

Certificate
  |-- belongs to Course
  |-- belongs to User (learner)

Notification
  |-- belongs to User
  |-- belongs to Course (nullable)

Membership
  |-- belongs to User
  |-- belongs to Organization
```
