-- Runs once, when the Postgres volume is first created.
-- The API, migrations, and tests all connect as `substratal`: a normal
-- (non-superuser) role that owns its database, so FORCE ROW LEVEL SECURITY
-- applies to it exactly as it does to the Aurora application user.
create role substratal login password 'substratal' createdb;
create database substratal owner substratal;
create database substratal_test owner substratal;
