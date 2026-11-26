-- Freemium: 1 sticker propio gratis; después, plan "ilimitado 30 días" pagado con Mercado Pago.
create table if not exists public.plans (
  user_id       uuid primary key references auth.users(id) on delete cascade,
  premium_until timestamptz,               -- ilimitado hasta esta fecha (null = sin plan)
  updated_at    timestamptz not null default now()
);

-- Un registro por pago de Mercado Pago: evita acreditar dos veces el mismo pago.
create table if not exists public.payments (
  id            text primary key,          -- id del pago en Mercado Pago
  user_id       uuid not null references auth.users(id) on delete cascade,
  status        text not null,
  amount        numeric(10,2) not null,
  currency      text not null,
  days          integer not null default 30,
  applied_at    timestamptz,               -- cuándo se sumaron los días al plan
  created_at    timestamptz not null default now()
);
create index if not exists payments_user_idx on public.payments (user_id, created_at desc);

alter table public.plans enable row level security;
alter table public.payments enable row level security;
grant select, insert, update, delete on public.plans, public.payments to service_role;
revoke all on public.plans, public.payments from anon, authenticated;
