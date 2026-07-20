-- Task 5 (Stage 6): public certificate verification.
--
-- learner_certificate's RLS policy (000004_learner_journey.up.sql) only
-- lets a row's own learner or their org's owner/teacher SELECT it —
-- correct for the authenticated "my certificates" endpoints, but the
-- product spec also requires a PUBLIC, unauthenticated verification page
-- ("is certificate X real?") that anyone with a certificate_id can hit
-- with no session at all. An anonymous request has no
-- app.current_user_id/app.current_org_id, so a normal RLS-scoped query
-- would just see zero rows — not a leak, but not usable either.
--
-- verify_certificate() is a SECURITY DEFINER function (same bypass-RLS
-- pattern as find_api_token_by_prefix in 000002_auth_tenancy.up.sql) that
-- runs with its owner's privileges, but its RETURNS TABLE signature is
-- the entire attack surface: it can only ever hand back learner_name,
-- course_title, and issued_at for the one certificate_id requested —
-- never the row's internal id, org_id, or pdf_storage_path, and never
-- more than one row. The Go handler for this endpoint queries it via the
-- raw pgxpool (no dbctx.Begin/RLS session context needed, since the
-- function itself does the privileged lookup).
--
-- EXECUTE on a newly created function defaults to PUBLIC in Postgres, so
-- no explicit GRANT is strictly required for this to work against local
-- Supabase (the app connects as the `postgres` role, which is also this
-- function's owner in local dev). The explicit GRANT below is kept anyway
-- as executable documentation: in any hosted environment where the app
-- connects as a non-owner, least-privilege role (Supabase's convention is
-- an `authenticator`/custom app role, not `postgres`), EXECUTE on this
-- function must be granted to that role explicitly, or this endpoint will
-- 42501-permission-denied in production even though it works locally.

CREATE FUNCTION verify_certificate(p_certificate_id TEXT)
RETURNS TABLE (learner_name TEXT, course_title TEXT, issued_at TIMESTAMPTZ)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT p.full_name, c.title, lc.issued_at
  FROM learner_certificate lc
  JOIN profiles p ON p.id = lc.learner_id
  JOIN courses c ON c.id = lc.course_id
  WHERE lc.certificate_id = p_certificate_id
$$;

GRANT EXECUTE ON FUNCTION verify_certificate(TEXT) TO PUBLIC;
