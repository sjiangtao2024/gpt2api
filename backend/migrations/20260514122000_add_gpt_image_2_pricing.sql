-- +goose Up
-- +goose StatementBegin
INSERT INTO `model` (`code`, `name`, `kind`, `provider`, `version`, `tags`, `point_per_unit`, `unit`, `group_code`, `min_plan`, `is_hot`, `sort`)
VALUES ('gpt-image-2', 'GPT Image 2', 'image', 'gpt', 'image', '图片,OpenAI', 0, 'image', 'gpt-image-default', 'free', 1, 4)
ON DUPLICATE KEY UPDATE
`name`=VALUES(`name`), `kind`=VALUES(`kind`), `provider`=VALUES(`provider`), `version`=VALUES(`version`),
`tags`=VALUES(`tags`), `point_per_unit`=VALUES(`point_per_unit`), `unit`=VALUES(`unit`), `group_code`=VALUES(`group_code`);

INSERT INTO `system_config` (`key`, `value`, `remark`)
SELECT 'billing.model_prices',
       '[]',
       '模型价格、上游映射和文字 token 计费'
WHERE NOT EXISTS (SELECT 1 FROM `system_config` WHERE `key`='billing.model_prices');

UPDATE `system_config`
SET `value` = JSON_ARRAY_APPEND(CAST(`value` AS JSON), '$', CAST('{"model_code":"gpt-image-2","name":"GPT Image 2","kind":"image","provider":"gpt","upstream_model":"gpt-image-2","unit_points":0,"enabled":true}' AS JSON))
WHERE `key`='billing.model_prices' AND JSON_SEARCH(CAST(`value` AS JSON), 'one', 'gpt-image-2', NULL, '$[*].model_code') IS NULL;
-- +goose StatementEnd

-- +goose Down
UPDATE `system_config`
SET `value` = JSON_REMOVE(
  CAST(`value` AS JSON),
  JSON_UNQUOTE(JSON_SEARCH(CAST(`value` AS JSON), 'one', 'gpt-image-2', NULL, '$[*].model_code'))
)
WHERE `key`='billing.model_prices' AND JSON_SEARCH(CAST(`value` AS JSON), 'one', 'gpt-image-2', NULL, '$[*].model_code') IS NOT NULL;

DELETE FROM `model` WHERE `code`='gpt-image-2';
