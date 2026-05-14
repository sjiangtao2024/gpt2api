-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS `generation_prompt_optimization` (
  `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `task_id`          CHAR(26) NOT NULL,
  `account_id`       BIGINT UNSIGNED DEFAULT NULL,
  `optimizer_model`  VARCHAR(64) NOT NULL,
  `mode`             VARCHAR(64) NOT NULL,
  `original_prompt`  MEDIUMTEXT NOT NULL,
  `optimized_prompt` MEDIUMTEXT DEFAULT NULL,
  `negative_prompt`  MEDIUMTEXT DEFAULT NULL,
  `brief_json`       JSON DEFAULT NULL,
  `status`           VARCHAR(24) NOT NULL,
  `error`            TEXT DEFAULT NULL,
  `latency_ms`       BIGINT NOT NULL DEFAULT 0,
  `created_at`       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_task_created` (`task_id`, `created_at`),
  KEY `idx_status_created` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='生成提示词优化记录';

INSERT INTO `system_config` (`key`, `value`, `remark`) VALUES
  ('image.prompt_optimizer.enabled', 'false', '图片广告提示词优化器开关'),
  ('image.prompt_optimizer.model', '"gpt-5.5"', '图片广告提示词优化器模型'),
  ('image.prompt_optimizer.timeout_seconds', '60', '图片广告提示词优化器超时秒数'),
  ('image.prompt_optimizer.mode', '"advertising_auto"', '图片广告提示词优化器策略模式'),
  ('image.prompt_optimizer.log_brief', 'true', '是否记录提示词优化 brief')
ON DUPLICATE KEY UPDATE `remark`=VALUES(`remark`);
-- +goose StatementEnd

-- +goose Down
DELETE FROM `system_config` WHERE `key` IN (
  'image.prompt_optimizer.enabled',
  'image.prompt_optimizer.model',
  'image.prompt_optimizer.timeout_seconds',
  'image.prompt_optimizer.mode',
  'image.prompt_optimizer.log_brief'
);
DROP TABLE IF EXISTS `generation_prompt_optimization`;
