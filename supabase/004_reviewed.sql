-- Moderación: marca de "ya revisado" en el panel de administración.
-- `reviewed` (generada) permite ordenar no revisados primero desde PostgREST.
alter table public.stickers
  add column if not exists reviewed_at timestamptz,
  add column if not exists reviewed boolean generated always as (reviewed_at is not null) stored;

create index if not exists stickers_review_idx on public.stickers (reviewed, created_at desc);
