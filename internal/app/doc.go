// Package app собирает зависимости server и worker из переменных окружения
// DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, RABBITMQ_URL,
// LOG_LEVEL и OTEL_EXPORTER_OTLP_ENDPOINT, без неё телеметрия не уходит.
//
// Остальные переменные необязательны. S3_BUCKET задаёт бакет, по умолчанию
// avatars. MAX_FILE_SIZE задаёт предел загрузки в байтах, не больше 10 МБ.
// ALLOWED_MIME_TYPES перечисляет через запятую разрешённые типы из
// image/jpeg, image/png и image/webp. TRUSTED_PROXIES задаёт число прокси
// перед server, тогда адрес клиента берётся из заголовка, который они
// дописывают, а не из адреса соединения.
package app
