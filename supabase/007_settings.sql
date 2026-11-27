-- Ajustes del sitio que se cambian desde /admin sin redeploy.
--   free_mode:   {"enabled": true|false}            -> crear gratis y sin registro (lo escribe la web)
--   bot_enabled: {"enabled": true|false}            -> interruptor del bot (lo escribe el bot al recibir la orden del admin)
--   bot_state:   {"connected": bool, "phone": "51…"} -> lo escribe el bot
create table if not exists public.settings (
  key        text primary key,
  value      jsonb not null default '{}'::jsonb,
  updated_at timestamptz not null default now()
);

alter table public.settings enable row level security;
grant select, insert, update, delete on public.settings to service_role;
revoke all on public.settings from anon, authenticated;
