DO $hanko_clear$
DECLARE
  s text := '__SCHEMA__';
  r record;
BEGIN
  FOR r IN SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
           WHERE n.nspname = s AND c.relkind IN ('v', 'm') LOOP
    EXECUTE format(CASE r.relkind WHEN 'm' THEN 'DROP MATERIALIZED VIEW IF EXISTS %I.%I CASCADE'
                                  ELSE 'DROP VIEW IF EXISTS %I.%I CASCADE' END, s, r.relname);
  END LOOP;
  FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
           WHERE n.nspname = s AND c.relkind IN ('r', 'p') AND NOT c.relispartition LOOP
    EXECUTE format('DROP TABLE IF EXISTS %I.%I CASCADE', s, r.relname);
  END LOOP;
  FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
           WHERE n.nspname = s AND c.relkind = 'S' LOOP
    EXECUTE format('DROP SEQUENCE IF EXISTS %I.%I CASCADE', s, r.relname);
  END LOOP;
  FOR r IN SELECT p.oid::regprocedure AS sig, p.prokind FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
           WHERE n.nspname = s LOOP
    EXECUTE format(CASE r.prokind WHEN 'a' THEN 'DROP AGGREGATE IF EXISTS %s CASCADE'
                                  WHEN 'p' THEN 'DROP PROCEDURE IF EXISTS %s CASCADE'
                                  ELSE 'DROP FUNCTION IF EXISTS %s CASCADE' END, r.sig);
  END LOOP;
  FOR r IN SELECT t.typname, t.typtype FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
           WHERE n.nspname = s AND t.typtype IN ('e', 'd', 'r', 'm', 'b')
             AND NOT EXISTS (SELECT 1 FROM pg_type e WHERE e.typarray = t.oid) LOOP
    EXECUTE format(CASE r.typtype WHEN 'd' THEN 'DROP DOMAIN IF EXISTS %I.%I CASCADE' ELSE 'DROP TYPE IF EXISTS %I.%I CASCADE' END, s, r.typname);
  END LOOP;
  FOR r IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace JOIN pg_class c ON c.oid = t.typrelid
           WHERE n.nspname = s AND t.typtype = 'c' AND c.relkind = 'c' LOOP
    EXECUTE format('DROP TYPE IF EXISTS %I.%I CASCADE', s, r.typname);
  END LOOP;
  FOR r IN SELECT cfgname FROM pg_ts_config c JOIN pg_namespace n ON n.oid = c.cfgnamespace WHERE n.nspname = s LOOP
    EXECUTE format('DROP TEXT SEARCH CONFIGURATION IF EXISTS %I.%I CASCADE', s, r.cfgname);
  END LOOP;
  FOR r IN SELECT collname FROM pg_collation c JOIN pg_namespace n ON n.oid = c.collnamespace WHERE n.nspname = s LOOP
    EXECUTE format('DROP COLLATION IF EXISTS %I.%I CASCADE', s, r.collname);
  END LOOP;
END
$hanko_clear$;
