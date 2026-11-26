-- Dueño opcional del sticker (usuarios que inician sesión con Google vía Supabase Auth).
-- Sin sesión, user_id queda en null como antes.
alter table public.stickers
  add column if not exists user_id uuid references auth.users(id) on delete set null;

create index if not exists stickers_user_idx on public.stickers (user_id, created_at desc);
