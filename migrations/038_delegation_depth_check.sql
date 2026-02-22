-- Prevent circular references and unbounded delegation depth at the database level.
-- Application already enforces MaxDelegationDepth=5, this adds a defense-in-depth constraint.
ALTER TABLE session_keys ADD CONSTRAINT chk_delegation_depth CHECK (depth >= 0 AND depth <= 5);
