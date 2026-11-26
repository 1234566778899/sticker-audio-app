-- Reseñas de usuarios: una por cuenta, con estrellas y comentario corto.
-- Solo se muestran en la portada las aprobadas por un administrador (/admin).
create table if not exists public.reviews (
  id          uuid primary key default gen_random_uuid(),
  user_id     uuid not null unique references auth.users(id) on delete cascade,
  name        text not null,                 -- nombre corto que se muestra ("Carlos O.")
  rating      smallint not null check (rating between 1 and 5),
  comment     text not null default '' check (char_length(comment) <= 280),
  lang        text not null default 'es',
  approved    boolean not null default false,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);
create index if not exists reviews_approved_idx on public.reviews (approved, updated_at desc);

alter table public.reviews enable row level security;
grant select, insert, update, delete on public.reviews to service_role;
revoke all on public.reviews from anon, authenticated;
