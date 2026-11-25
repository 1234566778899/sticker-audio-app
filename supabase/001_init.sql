-- Stickers con sonido — esquema inicial
create extension if not exists pgcrypto;

create table if not exists public.stickers (
  id          uuid primary key default gen_random_uuid(),
  title       text not null default '',
  image_path  text not null,            -- original en bucket "uploads"
  audio_path  text not null,            -- original en bucket "uploads"
  webp_path   text,                     -- procesado en bucket "stickers" (lo genera el bot)
  ogg_path    text,                     -- procesado en bucket "stickers"
  is_public   boolean not null default false,
  sends       integer not null default 0,
  created_at  timestamptz not null default now()
);

create table if not exists public.orders (
  code          text primary key,       -- ej. STK-4K7P
  sticker_id    uuid not null references public.stickers(id) on delete cascade,
  status        text not null default 'pending' check (status in ('pending','processing','sent','failed')),
  error         text,
  requester_jid text,
  created_at    timestamptz not null default now(),
  sent_at       timestamptz
);

create index if not exists stickers_public_idx on public.stickers (is_public, created_at desc);
create index if not exists orders_sticker_idx on public.orders (sticker_id);

-- Todo el acceso pasa por el servidor (clave secreta). Sin políticas = el público no puede leer/escribir directo.
alter table public.stickers enable row level security;
alter table public.orders enable row level security;

-- Buckets: "uploads" privado (originales), "stickers" público (previews y archivos finales)
insert into storage.buckets (id, name, public, file_size_limit)
values ('uploads', 'uploads', false, 26214400),
       ('stickers', 'stickers', true, 5242880)
on conflict (id) do nothing;
