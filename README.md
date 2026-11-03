# GophProfile

Сервис GophProfile хранит аватарки пользователей и раздаёт их другим сайтам.
Картинку загружают через REST API или веб-страницу, worker через RabbitMQ
делает миниатюры 100×100 и 300×300 и удаляет файлы.

Метаданные лежат в Postgres, файлы в MinIO.

## Документация

```text
POST   /api/v1/avatars                        # загрузка, файл в поле file или image формы
GET    /api/v1/avatars/{id}                   # файл, ?size=100x100|300x300 и ?format=jpeg|png|webp
GET    /api/v1/avatars/{id}/metadata          # размеры, формат, статус и миниатюры
GET    /api/v1/users/{user_id}/avatars        # все аватарки пользователя
GET    /api/v1/users/{user_id}/avatar         # текущая аватарка, те же size и format
DELETE /api/v1/avatars/{id}                   # удаление своей аватарки
DELETE /api/v1/users/{user_id}/avatar         # удаление своей текущей аватарки
GET    /health                                # состояние Postgres, S3 и RabbitMQ
GET    /web/upload                            # страница загрузки
GET    /web/gallery/{user_id}                 # галерея пользователя
```

Пользователь передаётся в заголовке `X-User-ID`. Форматы jpeg, png и webp,
файл до 10 МБ, с одного IP не больше 100 запросов в минуту.

## Запуск

Нужен Docker. Команды вызываются из корня репозитория.

```sh
docker compose up -d --build   # сервис на http://localhost:8080, RabbitMQ на http://127.0.0.1:15672, трейсы в Jaeger на http://127.0.0.1:16686, логи в OpenSearch Dashboards на http://127.0.0.1:5601, метрики в Prometheus на http://127.0.0.1:9090 и Grafana на http://127.0.0.1:3000
docker compose down            # остановка с сохранением данных
docker compose down -v         # остановка с удалением данных
```

## Логи

server и worker пишут JSON в stdout и через collector в OpenSearch. Порог задаёт `LOG_LEVEL`: `debug`, `info` (по умолчанию), `warn` или `error`.

- Поиск по запросу: trace ID из Jaeger ищется в Discover OpenSearch Dashboards запросом `traceId:<trace ID>`. Поле `attributes.trace_id` записи `uploading avatar` хранится массивом байтов, по нему не ищется.
- Фильтры: `severity.text` (уровень) и `resource.service.name` (`gophprofile-server` или `gophprofile-worker`).
- Алерт на ошибки: монитор «Ошибки в логах» в разделе Alerting раз в минуту ищет записи ERROR за последние 5 минут. Проверка: загрузить файл с верным заголовком PNG, но обрезанными данными. Worker отправит событие в очередь недоставленных, и через минуту появится активный алерт.

## Метрики

server и worker раз в 5 с отправляют метрики через collector, Prometheus снимает их с collector и глубину очередей с RabbitMQ. Grafana открывается без входа, дашборды лежат в папке GophProfile:

- Service overview: главные панели остальных трёх.
- RED: запросы, доля ответов 5xx и длительность HTTP по маршрутам.
- Resources: CPU, память и горутины server и worker, пул Postgres и очереди RabbitMQ.
- Business KPI: загрузки по статусу, время загрузки, занятое аватарками место.

Проверка: загрузить аватарку и через 15 с выполнить в Prometheus запросы `avatars_uploads_total{status="success"}` и `avatars_storage_bytes{user_id="<пользователь>"}`. Счётчик вырастет на 1, место на размер файла. Неверный формат увеличит `status="rejected"`, сбой хранилища или брокера `status="error"`.
