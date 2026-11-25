-- Solo el servidor (service_role / clave secreta) accede a las tablas. El público no.
grant select, insert, update, delete on public.stickers, public.orders to service_role;
revoke all on public.stickers, public.orders from anon, authenticated;
