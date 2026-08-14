-- Frozen App 1.5.1 / main schema 2 DDL.
-- Generated from published commit a4f33f9acd5ce7f85af1e8bdfd8f0a627ff500b0.
-- Do not regenerate this file from current Go models.

CREATE TABLE IF NOT EXISTS `stock_info` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`date` text,`time` text,`code` text,`name` text,`pre_price` real,`price` text,`volume` text,`amount` text,`open` text,`pre_close` text,`high` text,`low` text,`bid` text,`ask` text,`b1_p` text,`b1_v` text,`b2_p` text,`b2_v` text,`b3_p` text,`b3_v` text,`b4_p` text,`b4_v` text,`b5_p` text,`b5_v` text,`a1_p` text,`a1_v` text,`a2_p` text,`a2_v` text,`a3_p` text,`a3_v` text,`a4_p` text,`a4_v` text,`a5_p` text,`a5_v` text,`market` text,`ba` text,`ba_change` text,`change_percent` real,`change_price` real,`high_rate` real,`low_rate` real,`cost_price` real,`cost_volume` integer,`profit` real,`profit_amount` real,`profit_amount_today` real,`sort` integer,`alarm_change_percent` real,`alarm_price` real);
CREATE INDEX IF NOT EXISTS `idx_stock_info_name` ON `stock_info`(`name`);
CREATE INDEX IF NOT EXISTS `idx_stock_info_code` ON `stock_info`(`code`);
CREATE INDEX IF NOT EXISTS `idx_stock_info_time` ON `stock_info`(`time`);
CREATE INDEX IF NOT EXISTS `idx_stock_info_date` ON `stock_info`(`date`);
CREATE INDEX IF NOT EXISTS `idx_stock_info_deleted_at` ON `stock_info`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `tushare_stock_basic` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`ts_code` text,`symbol` text,`name` text,`area` text,`industry` text,`fullname` text,`ename` text,`cnspell` text,`market` text,`exchange` text,`curr_type` text,`list_status` text,`list_date` text,`delist_date` text,`is_hs` text,`act_name` text,`act_ent_type` text,`bk_name` text,`bk_code` text);
CREATE INDEX IF NOT EXISTS `idx_tushare_stock_basic_industry` ON `tushare_stock_basic`(`industry`);
CREATE INDEX IF NOT EXISTS `idx_tushare_stock_basic_name` ON `tushare_stock_basic`(`name`);
CREATE INDEX IF NOT EXISTS `idx_tushare_stock_basic_symbol` ON `tushare_stock_basic`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_tushare_stock_basic_ts_code` ON `tushare_stock_basic`(`ts_code`);
CREATE INDEX IF NOT EXISTS `idx_tushare_stock_basic_deleted_at` ON `tushare_stock_basic`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `followed_stock` (`stock_code` text,`name` text,`volume` integer,`cost_price` real,`price` real,`price_change` real,`change_percent` real,`alarm_change_percent` real,`alarm_price` real,`time` datetime,`sort` integer,`cron` text,`is_del` integer,`ai_config_id` integer);
CREATE TABLE IF NOT EXISTS `tushare_index_basic` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`ts_code` text,`symbol` text,`name` text,`full_name` text,`index_type` text,`index_category` text,`market` text,`list_date` text,`base_date` text,`base_point` real,`publisher` text,`weight_rule` text,`desc` text);
CREATE INDEX IF NOT EXISTS `idx_tushare_index_basic_name` ON `tushare_index_basic`(`name`);
CREATE INDEX IF NOT EXISTS `idx_tushare_index_basic_symbol` ON `tushare_index_basic`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_tushare_index_basic_ts_code` ON `tushare_index_basic`(`ts_code`);
CREATE INDEX IF NOT EXISTS `idx_tushare_index_basic_deleted_at` ON `tushare_index_basic`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`tushare_token` text,`local_push_enable` numeric,`ding_push_enable` numeric,`ding_robot` text,`yield_email_enable` numeric,`yield_email_to` text,`yield_email_from` text,`yield_email_smtp_host` text,`yield_email_smtp_port` integer,`yield_email_smtp_username` text,`yield_email_smtp_password` text,`yield_email_cron_enabled` numeric,`yield_email_cron_times` text,`market_summary_email_enable` numeric,`update_basic_info_on_start` numeric,`refresh_interval` integer,`open_ai_enable` numeric,`prompt` text,`check_update` numeric,`question_template` text,`crawl_time_out` integer,`k_days` integer,`enable_danmu` numeric,`browser_path` text,`enable_news` numeric,`dark_theme` numeric,`browser_pool_size` integer,`enable_fund` numeric,`enable_push_news` numeric,`enable_only_push_red_news` numeric,`http_proxy` text,`http_proxy_enabled` numeric,`force_no_proxy_for_fetch` numeric DEFAULT true,`enable_agent` numeric,`qgqp_b_id` text,`market_summary_cron_enabled` numeric DEFAULT true,`market_summary_cron_times` text DEFAULT "09:40,11:30,14:30",`minute_provider_mode` text DEFAULT "public",`minute_long_history_hint_enabled` numeric DEFAULT true,`private_minute_enabled` numeric,`private_minute_base_url` text,`private_minute_api_key` text,`private_minute_timeout_sec` integer,`private_minute_min_interval` integer,`private_minute_proxy_mode` text DEFAULT "disable",`private_minute_level` text DEFAULT "1min",`akshare_enabled` numeric DEFAULT true,`sina_minute_enabled` numeric DEFAULT true,`tencent_minute_enabled` numeric DEFAULT true,`eastmoney_minute_enabled` numeric DEFAULT true,`akshare_minute_source_mode` text DEFAULT "auto");
CREATE INDEX IF NOT EXISTS `idx_settings_deleted_at` ON `settings`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `ai_response_result` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`chat_id` text,`provider_name` text,`model_name` text,`stock_code` text,`stock_name` text,`question` text,`content` text,`is_del` integer);
CREATE INDEX IF NOT EXISTS `idx_ai_response_result_deleted_at` ON `ai_response_result`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `agent_chat_session` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`session_id` text,`title` text,`ai_config_id` integer,`model_name` text,`last_message_at` datetime,`message_count` integer,`is_pinned` numeric,`is_del` integer);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_session_is_pinned` ON `agent_chat_session`(`is_pinned`);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_session_last_message_at` ON `agent_chat_session`(`last_message_at`);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_session_ai_config_id` ON `agent_chat_session`(`ai_config_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_agent_chat_session_session_id` ON `agent_chat_session`(`session_id`);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_session_deleted_at` ON `agent_chat_session`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `agent_chat_message` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`session_id` text,`role` text,`content` text,`reasoning` text,`seq` integer,`is_del` integer);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_message_seq` ON `agent_chat_message`(`seq`);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_message_role` ON `agent_chat_message`(`role`);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_message_session_id` ON `agent_chat_message`(`session_id`);
CREATE INDEX IF NOT EXISTS `idx_agent_chat_message_deleted_at` ON `agent_chat_message`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `stock_base_info_hk` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`code` text,`name` text,`full_name` text,`e_name` text,`is_del` integer,`bk_name` text,`bk_code` text);
CREATE INDEX IF NOT EXISTS `idx_stock_base_info_hk_deleted_at` ON `stock_base_info_hk`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `stock_base_info_us` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`code` text,`name` text,`full_name` text,`e_name` text,`exchange` text,`type` text,`is_del` integer,`bk_name` text,`bk_code` text);
CREATE INDEX IF NOT EXISTS `idx_stock_base_info_us_deleted_at` ON `stock_base_info_us`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `followed_fund` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`code` text,`name` text,`net_unit_value` real,`net_unit_value_date` text,`net_estimated_unit` real,`net_estimated_time` text,`net_accumulated` real,`net_estimated_rate` real);
CREATE INDEX IF NOT EXISTS `idx_followed_fund_code` ON `followed_fund`(`code`);
CREATE INDEX IF NOT EXISTS `idx_followed_fund_deleted_at` ON `followed_fund`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `fund_basic` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`code` text,`name` text,`full_name` text,`type` text,`establishment` text,`scale` text,`company` text,`manager` text,`rating` text,`tracking_target` text,`net_unit_value` real,`net_unit_value_date` text,`net_estimated_unit` real,`net_estimated_time` text,`net_accumulated` real,`net_growth1` real,`net_growth3` real,`net_growth6` real,`net_growth12` real,`net_growth36` real,`net_growth60` real,`net_growth_ytd` real,`net_growth_all` real,CONSTRAINT `fk_followed_fund_fund_basic` FOREIGN KEY (`code`) REFERENCES `followed_fund`(`code`));
CREATE INDEX IF NOT EXISTS `idx_fund_basic_code` ON `fund_basic`(`code`);
CREATE INDEX IF NOT EXISTS `idx_fund_basic_deleted_at` ON `fund_basic`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `prompt_templates` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`name` text,`content` text,`type` text);
CREATE TABLE IF NOT EXISTS `stock_groups` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`name` text,`sort` integer);
CREATE INDEX IF NOT EXISTS `idx_stock_groups_name` ON `stock_groups`(`name`);
CREATE INDEX IF NOT EXISTS `idx_stock_groups_deleted_at` ON `stock_groups`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `group_stock_info` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`stock_code` text,`group_id` integer,CONSTRAINT `fk_group_stock_info_group_info` FOREIGN KEY (`group_id`) REFERENCES `stock_groups`(`id`),CONSTRAINT `fk_followed_stock_groups` FOREIGN KEY (`stock_code`) REFERENCES `followed_stock`(`stock_code`));
CREATE INDEX IF NOT EXISTS `idx_group_stock_info_group_id` ON `group_stock_info`(`group_id`);
CREATE INDEX IF NOT EXISTS `idx_group_stock_info_stock_code` ON `group_stock_info`(`stock_code`);
CREATE INDEX IF NOT EXISTS `idx_group_stock_info_deleted_at` ON `group_stock_info`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `tags` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`name` text,`type` text);
CREATE INDEX IF NOT EXISTS `idx_tags_deleted_at` ON `tags`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `telegraph_list` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`time` text,`data_time` datetime,`title` text,`content` text,`is_red` numeric,`url` text,`source` text,`sentiment_result` text);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_sentiment_result` ON `telegraph_list`(`sentiment_result`);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_source` ON `telegraph_list`(`source`);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_is_red` ON `telegraph_list`(`is_red`);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_content` ON `telegraph_list`(`content`);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_title` ON `telegraph_list`(`title`);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_data_time` ON `telegraph_list`(`data_time`);
CREATE INDEX IF NOT EXISTS `idx_telegraph_list_deleted_at` ON `telegraph_list`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `telegraph_tags` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`tag_id` integer,`telegraph_id` integer);
CREATE INDEX IF NOT EXISTS `idx_telegraph_tags_deleted_at` ON `telegraph_tags`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `long_tiger_rank` (`accumamount` real,`billboardbuyamt` real,`billboarddealamt` real,`billboardnetamt` real,`billboardsellamt` real,`changerate` real,`closeprice` real,`dealamountratio` real,`dealnetratio` real,`explain` text,`explanation` text,`freemarketcap` real,`secucode` text,`sec_uri_tycode` text,`sec_uri_tynameabbr` text,`sec_uri_tytypecode` text,`tradedate` text,`turnoverrate` real);
CREATE INDEX IF NOT EXISTS `idx_long_tiger_rank_tradedate` ON `long_tiger_rank`(`tradedate`);
CREATE INDEX IF NOT EXISTS `idx_long_tiger_rank_secucode` ON `long_tiger_rank`(`secucode`);
CREATE TABLE IF NOT EXISTS `ai_config` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`sort` integer,`name` text,`base_url` text,`api_key` text,`model_name` text,`api_protocol` text DEFAULT "chat_completions",`max_tokens` integer,`temperature` real,`time_out` integer,`http_proxy` text,`http_proxy_enabled` numeric);
CREATE INDEX IF NOT EXISTS `idx_ai_config_sort` ON `ai_config`(`sort`);
CREATE TABLE IF NOT EXISTS `bk_dict` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`bk_code` text,`bk_name` text,`first_letter` text,`fubk_code` text,`publish_code` text);
CREATE INDEX IF NOT EXISTS `idx_bk_dict_deleted_at` ON `bk_dict`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `word_analyzes` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`data_time` datetime,`word` text,`frequency` integer,`weight` real,`score` real);
CREATE INDEX IF NOT EXISTS `idx_word_analyzes_data_time` ON `word_analyzes`(`data_time`);
CREATE INDEX IF NOT EXISTS `idx_word_analyzes_deleted_at` ON `word_analyzes`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `sentiment_result_analyzes` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`data_time` datetime,`score` real,`category` integer,`positive_count` integer,`negative_count` integer,`description` text);
CREATE INDEX IF NOT EXISTS `idx_sentiment_result_analyzes_data_time` ON `sentiment_result_analyzes`(`data_time`);
CREATE INDEX IF NOT EXISTS `idx_sentiment_result_analyzes_deleted_at` ON `sentiment_result_analyzes`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `ai_recommend_stocks` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`data_time` datetime,`provider_name` text,`model_name` text,`stock_code` text,`stock_name` text,`bk_code` text,`bk_name` text,`stock_price` text,`stock_current_price` text,`stock_current_price_time` text,`stock_close_price` text,`stock_pre_price` text,`recommend_reason` text,`recommend_buy_price` text,`recommend_buy_price_min` real,`recommend_buy_price_max` real,`recommend_stop_profit_price` text,`recommend_stop_profit_price_min` real,`recommend_stop_profit_price_max` real,`recommend_stop_loss_price` text,`recommend_category` text,`execution_state` text,`buy_signal` text,`buy_signal_detail` text,`sell_signal` text,`sell_signal_detail` text,`invalid_signal` text,`core_catalyst` text,`key_evidence` text,`evidence_sources` text,`invalid_condition` text,`observe_price` text,`focus_price` text,`expected_cycle` text,`event_strength` integer,`capital_confirmation` integer,`fundamental_fit` integer,`technical_fit` integer,`activation_rule_json` text,`activation_rule_version` text,`activation_rule_source` text,`activation_status` text,`activation_invalid_reason` text,`recommend_status` text,`summary_version` text,`strategy_run_id` text,`strategy_rule_id` text,`risk_remarks` text,`remarks` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_stocks_strategy_rule_id` ON `ai_recommend_stocks`(`strategy_rule_id`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_stocks_strategy_run_id` ON `ai_recommend_stocks`(`strategy_run_id`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_stocks_activation_status` ON `ai_recommend_stocks`(`activation_status`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_stocks_data_time` ON `ai_recommend_stocks`(`data_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_stocks_deleted_at` ON `ai_recommend_stocks`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `ai_recommend_opening_review` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`recommend_id` integer,`stock_code` text,`stock_name` text,`trade_date` text,`review_scope` text,`review_phase` text,`opening_price` real,`auction_price` real,`minute_price` real,`minute_volume` real,`minute_amount` real,`gap_type` text,`action` text,`reason` text,`suggested_stop_loss` real,`suggested_take_profit` real,`model_name` text,`raw_summary` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_opening_review_action` ON `ai_recommend_opening_review`(`action`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_opening_review_review_phase` ON `ai_recommend_opening_review`(`review_phase`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_opening_review_review_scope` ON `ai_recommend_opening_review`(`review_scope`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_opening_review_trade_date` ON `ai_recommend_opening_review`(`trade_date`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_opening_review_stock_code` ON `ai_recommend_opening_review`(`stock_code`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_rec_opening_review_key` ON `ai_recommend_opening_review`(`recommend_id`,`trade_date`,`review_scope`,`review_phase`);
CREATE TABLE IF NOT EXISTS `ai_recommend_yield_state` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`stock_code` text,`stock_name` text,`model_names` text,`bk_name` text,`recommend_count` integer,`recommend_category` text,`recommend_time` datetime,`signal_time` datetime,`activation_status` text,`activation_time` datetime,`activation_price` real,`buy_time` datetime,`buy_amount` real,`stop_profit_amount` real,`stop_loss_amount` real,`sell_amount_text` text,`position_status` text,`sell_time` datetime,`realized_sell_amount` real,`current_price` real,`current_price_time` text,`yield_rate` real,`yield_rate_text` text,`data_status` text,`data_status_reason` text,`last_minute_ts` datetime,`last_recalc_at` datetime,`minute_cache_start` datetime,`minute_cache_end` datetime,`minute_cache_source` text,`minute_cache_updated` datetime,`frozen` numeric,`total_scope_start` text,`total_scope_end` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_frozen` ON `ai_recommend_yield_state`(`frozen`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_minute_cache_end` ON `ai_recommend_yield_state`(`minute_cache_end`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_minute_cache_start` ON `ai_recommend_yield_state`(`minute_cache_start`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_last_recalc_at` ON `ai_recommend_yield_state`(`last_recalc_at`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_last_minute_ts` ON `ai_recommend_yield_state`(`last_minute_ts`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_buy_time` ON `ai_recommend_yield_state`(`buy_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_activation_time` ON `ai_recommend_yield_state`(`activation_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_activation_status` ON `ai_recommend_yield_state`(`activation_status`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_signal_time` ON `ai_recommend_yield_state`(`signal_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_recommend_time` ON `ai_recommend_yield_state`(`recommend_time`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_recommend_yield_state_stock_code` ON `ai_recommend_yield_state`(`stock_code`);
CREATE TABLE IF NOT EXISTS `ai_recommend_yield_override` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`recommend_id` integer,`stock_code` text,`review_round` integer,`review_source` text,`reviewed_at` datetime,`activation_status_override` text,`recommend_buy_price` text,`recommend_buy_price_min` real,`recommend_buy_price_max` real,`recommend_stop_profit_price` text,`recommend_stop_profit_price_min` real,`recommend_stop_profit_price_max` real,`recommend_stop_loss_price` text,`buy_signal` text,`buy_signal_detail` text,`activation_rule_json` text,`activation_rule_version` text,`activation_rule_source` text,`invalid_signal` text,`invalid_condition` text,`data_status_reason` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_override_activation_status_override` ON `ai_recommend_yield_override`(`activation_status_override`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_override_reviewed_at` ON `ai_recommend_yield_override`(`reviewed_at`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_override_stock_code` ON `ai_recommend_yield_override`(`stock_code`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_recommend_yield_override_recommend_id` ON `ai_recommend_yield_override`(`recommend_id`);
CREATE TABLE IF NOT EXISTS `ai_recommend_yield_record_state` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`recommend_id` integer,`stock_code` text,`stock_name` text,`model_name` text,`bk_name` text,`recommend_category` text,`recommend_time` datetime,`signal_time` datetime,`activation_status` text,`activation_time` datetime,`activation_price` real,`buy_time` datetime,`buy_amount` real,`stop_profit_amount` real,`stop_loss_amount` real,`sell_amount_text` text,`position_status` text,`sell_time` datetime,`realized_sell_amount` real,`current_price` real,`current_price_time` text,`yield_rate` real,`yield_rate_text` text,`data_status` text,`data_status_reason` text,`last_minute_ts` datetime,`last_recalc_at` datetime,`minute_cache_start` datetime,`minute_cache_end` datetime,`minute_cache_source` text,`minute_cache_updated` datetime,`frozen` numeric,`total_scope_start` text,`total_scope_end` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_frozen` ON `ai_recommend_yield_record_state`(`frozen`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_minute_cache_end` ON `ai_recommend_yield_record_state`(`minute_cache_end`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_minute_cache_start` ON `ai_recommend_yield_record_state`(`minute_cache_start`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_last_recalc_at` ON `ai_recommend_yield_record_state`(`last_recalc_at`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_last_minute_ts` ON `ai_recommend_yield_record_state`(`last_minute_ts`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_buy_time` ON `ai_recommend_yield_record_state`(`buy_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_activation_time` ON `ai_recommend_yield_record_state`(`activation_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_activation_status` ON `ai_recommend_yield_record_state`(`activation_status`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_signal_time` ON `ai_recommend_yield_record_state`(`signal_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_recommend_time` ON `ai_recommend_yield_record_state`(`recommend_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_stock_code` ON `ai_recommend_yield_record_state`(`stock_code`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_recommend_yield_record_state_recommend_id` ON `ai_recommend_yield_record_state`(`recommend_id`);
CREATE TABLE IF NOT EXISTS `ai_recommend_yield_meta` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`last_full_recalc_at` datetime,`last_yield_email_sent_at` datetime,`last_yield_email_sent_reason` text,`last_query_recalc_at` datetime,`query_cooldown_until` datetime,`last_manual_download_at` datetime,`manual_cooldown_until` datetime,`recalc_in_progress` numeric,`recalc_total` integer,`recalc_done` integer,`recalc_progress` integer,`download_in_progress` numeric,`download_total` integer,`download_done` integer,`download_progress` integer,`last_download_error` text,`last_error` text,`current_trade_date` text,`akshare_ready` numeric,`akshare_checked_at` datetime,`akshare_install_error` text,`frozen_sell_price_fix_version` text,`last_manual_finished_at` datetime,`last_manual_scope_count` integer,`last_manual_prefetch_ms` integer,`last_manual_recalc_ms` integer,`last_manual_total_ms` integer,`last_manual_sqlite_busy_count` integer,`last_manual_provider_summary` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_last_manual_finished_at` ON `ai_recommend_yield_meta`(`last_manual_finished_at`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_download_in_progress` ON `ai_recommend_yield_meta`(`download_in_progress`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_recalc_in_progress` ON `ai_recommend_yield_meta`(`recalc_in_progress`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_manual_cooldown_until` ON `ai_recommend_yield_meta`(`manual_cooldown_until`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_last_manual_download_at` ON `ai_recommend_yield_meta`(`last_manual_download_at`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_query_cooldown_until` ON `ai_recommend_yield_meta`(`query_cooldown_until`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_last_query_recalc_at` ON `ai_recommend_yield_meta`(`last_query_recalc_at`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_meta_last_yield_email_sent_at` ON `ai_recommend_yield_meta`(`last_yield_email_sent_at`);
CREATE TABLE IF NOT EXISTS `ai_recommend_yield_dirty_code` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`stock_code` text,`recommend_id` integer,`reason` text,`mode_needed` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_dirty_code_mode_needed` ON `ai_recommend_yield_dirty_code`(`mode_needed`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_dirty_code_recommend_id` ON `ai_recommend_yield_dirty_code`(`recommend_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_recommend_yield_dirty_scope` ON `ai_recommend_yield_dirty_code`(`stock_code`,`recommend_id`,`mode_needed`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_yield_dirty_code_stock_code` ON `ai_recommend_yield_dirty_code`(`stock_code`);
CREATE TABLE IF NOT EXISTS `ai_recommend_minute_bar` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`stock_code` text,`trade_time` datetime,`open` real,`high` real,`low` real,`close` real,`volume` real,`amount` real,`source` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_minute_bar_trade_time` ON `ai_recommend_minute_bar`(`trade_time`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_rec_minute_code_time` ON `ai_recommend_minute_bar`(`stock_code`,`trade_time`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_minute_bar_stock_code` ON `ai_recommend_minute_bar`(`stock_code`);
CREATE TABLE IF NOT EXISTS `ai_recommend_daily_bar` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`stock_code` text,`trade_date` datetime,`open` real,`high` real,`low` real,`close` real,`volume` real,`amount` real,`source` text);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_daily_bar_trade_date` ON `ai_recommend_daily_bar`(`trade_date`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_ai_rec_daily_code_date` ON `ai_recommend_daily_bar`(`stock_code`,`trade_date`);
CREATE INDEX IF NOT EXISTS `idx_ai_recommend_daily_bar_stock_code` ON `ai_recommend_daily_bar`(`stock_code`);
CREATE TABLE IF NOT EXISTS `cron_task_runs` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`task_name` text,`triggered_at` datetime,`status` text,`attempts` integer,`ai_config_id` integer,`model_name` text,`chat_id` text,`error_message` text);
CREATE INDEX IF NOT EXISTS `idx_cron_task_runs_ai_config_id` ON `cron_task_runs`(`ai_config_id`);
CREATE INDEX IF NOT EXISTS `idx_cron_task_runs_status` ON `cron_task_runs`(`status`);
CREATE INDEX IF NOT EXISTS `idx_cron_task_runs_triggered_at` ON `cron_task_runs`(`triggered_at`);
CREATE INDEX IF NOT EXISTS `idx_cron_task_runs_task_name` ON `cron_task_runs`(`task_name`);
CREATE INDEX IF NOT EXISTS `idx_cron_task_runs_deleted_at` ON `cron_task_runs`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `email_send_logs` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`send_type` text,`triggered_at` datetime,`status` text,`recipients` text,`subject` text,`error_message` text,`report_stock_code` text,`report_stock_name` text,`report_created_at` datetime,`attachment_names` text,`attachment_count` integer,`attachment_bytes` integer,`extra_summary` text);
CREATE INDEX IF NOT EXISTS `idx_email_send_logs_report_created_at` ON `email_send_logs`(`report_created_at`);
CREATE INDEX IF NOT EXISTS `idx_email_send_logs_report_stock_code` ON `email_send_logs`(`report_stock_code`);
CREATE INDEX IF NOT EXISTS `idx_email_send_logs_status` ON `email_send_logs`(`status`);
CREATE INDEX IF NOT EXISTS `idx_email_send_logs_triggered_at` ON `email_send_logs`(`triggered_at`);
CREATE INDEX IF NOT EXISTS `idx_email_send_logs_send_type` ON `email_send_logs`(`send_type`);
CREATE INDEX IF NOT EXISTS `idx_email_send_logs_deleted_at` ON `email_send_logs`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `market_summary_run_diagnostics` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`run_id` text,`summary_version` text,`run_slot` text,`started_at` datetime,`finished_at` datetime,`indicator_candidate_count` integer,`indicator_ai_input_count` integer,`discovery_candidate_count` integer,`verified_candidate_count` integer,`ai_output_count_first` integer,`ai_output_count_second` integer,`saved_count` integer,`production_count` integer,`analysis_only_count` integer,`blocked_count` integer,`blocked_reason_top` text,`production_downgrade_reason_top` text,`empty_run` numeric,`notes_json` text);
CREATE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_empty_run` ON `market_summary_run_diagnostics`(`empty_run`);
CREATE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_finished_at` ON `market_summary_run_diagnostics`(`finished_at`);
CREATE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_started_at` ON `market_summary_run_diagnostics`(`started_at`);
CREATE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_run_slot` ON `market_summary_run_diagnostics`(`run_slot`);
CREATE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_summary_version` ON `market_summary_run_diagnostics`(`summary_version`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_run_id` ON `market_summary_run_diagnostics`(`run_id`);
CREATE INDEX IF NOT EXISTS `idx_market_summary_run_diagnostics_deleted_at` ON `market_summary_run_diagnostics`(`deleted_at`);
CREATE TABLE IF NOT EXISTS `strategy_run_snapshot` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`run_id` text NOT NULL,`strategy_version` text NOT NULL,`trade_date` text NOT NULL,`run_slot` text,`started_at` datetime NOT NULL,`as_of` datetime NOT NULL,`data_cutoff_at` datetime NOT NULL,`decision_at` datetime NOT NULL,`generated_at` datetime NOT NULL,`valid_from_at` datetime,`mode` text,`config_hash` text,`input_hash` text,`candidate_count` integer,`rule_count` integer,`order_event_count` integer,`security_snapshot_count` integer,`corporate_action_count` integer,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_frozen_at` ON `strategy_run_snapshot`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_snapshot_hash` ON `strategy_run_snapshot`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_input_hash` ON `strategy_run_snapshot`(`input_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_config_hash` ON `strategy_run_snapshot`(`config_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_mode` ON `strategy_run_snapshot`(`mode`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_valid_from_at` ON `strategy_run_snapshot`(`valid_from_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_decision_at` ON `strategy_run_snapshot`(`decision_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_data_cutoff_at` ON `strategy_run_snapshot`(`data_cutoff_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_as_of` ON `strategy_run_snapshot`(`as_of`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_started_at` ON `strategy_run_snapshot`(`started_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_run_version_date` ON `strategy_run_snapshot`(`strategy_version`,`trade_date`,`run_slot`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_run_snapshot_run_id` ON `strategy_run_snapshot`(`run_id`);
CREATE TABLE IF NOT EXISTS `strategy_candidate_snapshot` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`candidate_id` text NOT NULL,`run_id` text NOT NULL,`strategy_version` text NOT NULL,`trade_date` text NOT NULL,`symbol` text NOT NULL,`name` text,`sector` text,`market` text,`rank` integer,`pre_verify_rank` integer,`final_rank` integer,`decision` text,`score` real,`eligible` numeric,`rejection_reason` text,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_frozen_at` ON `strategy_candidate_snapshot`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_snapshot_hash` ON `strategy_candidate_snapshot`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_eligible` ON `strategy_candidate_snapshot`(`eligible`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_score` ON `strategy_candidate_snapshot`(`score`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_decision` ON `strategy_candidate_snapshot`(`decision`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_final_rank` ON `strategy_candidate_snapshot`(`final_rank`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_pre_verify_rank` ON `strategy_candidate_snapshot`(`pre_verify_rank`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_rank` ON `strategy_candidate_snapshot`(`rank`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_market` ON `strategy_candidate_snapshot`(`market`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_sector` ON `strategy_candidate_snapshot`(`sector`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_symbol` ON `strategy_candidate_snapshot`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_version_date` ON `strategy_candidate_snapshot`(`strategy_version`,`trade_date`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_candidate_run_symbol` ON `strategy_candidate_snapshot`(`run_id`,`symbol`);
CREATE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_run_id` ON `strategy_candidate_snapshot`(`run_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_candidate_snapshot_candidate_id` ON `strategy_candidate_snapshot`(`candidate_id`);
CREATE TABLE IF NOT EXISTS `strategy_rule_snapshot` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`rule_id` text NOT NULL,`run_id` text NOT NULL,`candidate_id` text,`strategy_version` text NOT NULL,`trade_date` text NOT NULL,`symbol` text NOT NULL,`rule_version` text,`rule_type` text,`path` text,`valid_from_at` datetime NOT NULL,`expires_at` datetime,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_frozen_at` ON `strategy_rule_snapshot`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_snapshot_hash` ON `strategy_rule_snapshot`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_expires_at` ON `strategy_rule_snapshot`(`expires_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_valid_from_at` ON `strategy_rule_snapshot`(`valid_from_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_path` ON `strategy_rule_snapshot`(`path`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_rule_type` ON `strategy_rule_snapshot`(`rule_type`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_rule_version` ON `strategy_rule_snapshot`(`rule_version`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_symbol` ON `strategy_rule_snapshot`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_version_date` ON `strategy_rule_snapshot`(`strategy_version`,`trade_date`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_candidate_id` ON `strategy_rule_snapshot`(`candidate_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_rule_run_symbol_path` ON `strategy_rule_snapshot`(`run_id`,`symbol`,`path`);
CREATE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_run_id` ON `strategy_rule_snapshot`(`run_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_rule_snapshot_rule_id` ON `strategy_rule_snapshot`(`rule_id`);
CREATE TABLE IF NOT EXISTS `strategy_order_event` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`event_id` text NOT NULL,`run_id` text NOT NULL,`rule_id` text,`strategy_version` text NOT NULL,`trade_date` text NOT NULL,`symbol` text NOT NULL,`event_type` text NOT NULL,`sequence` integer NOT NULL,`event_at` datetime NOT NULL,`price` real,`quantity` real,`cash_amount` real,`adjustment_factor` real,`fees` real,`reason` text,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_frozen_at` ON `strategy_order_event`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_snapshot_hash` ON `strategy_order_event`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_event_at` ON `strategy_order_event`(`event_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_event_type` ON `strategy_order_event`(`event_type`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_symbol` ON `strategy_order_event`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_version_date` ON `strategy_order_event`(`strategy_version`,`trade_date`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_rule_id` ON `strategy_order_event`(`rule_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_order_run_rule_sequence` ON `strategy_order_event`(`run_id`,`rule_id`,`sequence`);
CREATE INDEX IF NOT EXISTS `idx_strategy_order_event_run_id` ON `strategy_order_event`(`run_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_order_event_event_id` ON `strategy_order_event`(`event_id`);
CREATE TABLE IF NOT EXISTS `strategy_backtest_run` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`backtest_id` text NOT NULL,`strategy_version` text NOT NULL,`start_date` text NOT NULL,`end_date` text NOT NULL,`input_hash` text NOT NULL,`status` text NOT NULL,`run_snapshot_count` integer,`candidate_snapshot_count` integer,`rule_snapshot_count` integer,`order_event_count` integer,`security_snapshot_count` integer,`corporate_action_count` integer,`trade_count` integer,`metric_count` integer,`summary_json` text NOT NULL,`started_at` datetime NOT NULL,`completed_at` datetime NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_run_frozen_at` ON `strategy_backtest_run`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_run_completed_at` ON `strategy_backtest_run`(`completed_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_run_started_at` ON `strategy_backtest_run`(`started_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_run_status` ON `strategy_backtest_run`(`status`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_run_input_hash` ON `strategy_backtest_run`(`input_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_version_range` ON `strategy_backtest_run`(`strategy_version`,`start_date`,`end_date`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_backtest_run_backtest_id` ON `strategy_backtest_run`(`backtest_id`);
CREATE TABLE IF NOT EXISTS `strategy_backtest_trade` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`trade_id` text NOT NULL,`backtest_id` text NOT NULL,`strategy_version` text NOT NULL,`sequence` integer NOT NULL,`symbol` text NOT NULL,`entry_at` datetime NOT NULL,`exit_at` datetime,`entry_price` real,`exit_price` real,`quantity` real,`fees` real,`gross_pn_l` real,`net_pn_l` real,`return_pct` real,`exit_reason` text,`source_order_event_ids` text,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_frozen_at` ON `strategy_backtest_trade`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_snapshot_hash` ON `strategy_backtest_trade`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_exit_reason` ON `strategy_backtest_trade`(`exit_reason`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_exit_at` ON `strategy_backtest_trade`(`exit_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_entry_at` ON `strategy_backtest_trade`(`entry_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_symbol` ON `strategy_backtest_trade`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_strategy_version` ON `strategy_backtest_trade`(`strategy_version`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_seq` ON `strategy_backtest_trade`(`backtest_id`,`sequence`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_backtest_id` ON `strategy_backtest_trade`(`backtest_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_backtest_trade_trade_id` ON `strategy_backtest_trade`(`trade_id`);
CREATE TABLE IF NOT EXISTS `strategy_backtest_metric` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`metric_id` text NOT NULL,`backtest_id` text NOT NULL,`name` text NOT NULL,`scope` text NOT NULL DEFAULT "summary",`value` real,`value_text` text,`unit` text,`ordinal` integer,`payload_json` text,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_metric_frozen_at` ON `strategy_backtest_metric`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_metric_ordinal` ON `strategy_backtest_metric`(`ordinal`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_metric_name` ON `strategy_backtest_metric`(`name`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_backtest_metric_key` ON `strategy_backtest_metric`(`backtest_id`,`name`,`scope`);
CREATE INDEX IF NOT EXISTS `idx_strategy_backtest_metric_backtest_id` ON `strategy_backtest_metric`(`backtest_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_strategy_backtest_metric_metric_id` ON `strategy_backtest_metric`(`metric_id`);
CREATE TABLE IF NOT EXISTS `security_master_history` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`record_id` text NOT NULL,`run_id` text NOT NULL,`snapshot_version` text NOT NULL,`symbol` text NOT NULL,`name` text,`market` text,`exchange` text,`board` text,`sector` text,`industry` text,`currency` text,`status` text,`is_st` numeric,`is_suspended` numeric,`listed_at` datetime,`delisted_at` datetime,`effective_from` datetime NOT NULL,`effective_to` datetime,`source` text,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_frozen_at` ON `security_master_history`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_snapshot_hash` ON `security_master_history`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_source` ON `security_master_history`(`source`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_effective_to` ON `security_master_history`(`effective_to`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_effective_from` ON `security_master_history`(`effective_from`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_delisted_at` ON `security_master_history`(`delisted_at`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_listed_at` ON `security_master_history`(`listed_at`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_is_suspended` ON `security_master_history`(`is_suspended`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_is_st` ON `security_master_history`(`is_st`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_status` ON `security_master_history`(`status`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_industry` ON `security_master_history`(`industry`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_sector` ON `security_master_history`(`sector`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_board` ON `security_master_history`(`board`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_exchange` ON `security_master_history`(`exchange`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_market` ON `security_master_history`(`market`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_symbol` ON `security_master_history`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_security_master_version_symbol` ON `security_master_history`(`snapshot_version`,`symbol`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_security_master_run_symbol_effective` ON `security_master_history`(`run_id`,`symbol`,`effective_from`);
CREATE INDEX IF NOT EXISTS `idx_security_master_history_run_id` ON `security_master_history`(`run_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_security_master_run_record` ON `security_master_history`(`run_id`,`record_id`);
CREATE TABLE IF NOT EXISTS `corporate_action_event` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`event_id` text NOT NULL,`run_id` text NOT NULL,`snapshot_version` text NOT NULL,`symbol` text NOT NULL,`action_type` text NOT NULL,`announced_at` datetime,`source_at` datetime,`available_at` datetime,`observation_status` text,`coverage_start` datetime,`coverage_end` datetime,`ex_date` datetime NOT NULL,`record_date` datetime,`pay_date` datetime,`cash_dividend` real,`split_ratio` real,`bonus_ratio` real,`rights_ratio` real,`rights_price` real,`adjustment_factor` real,`currency` text,`source` text,`snapshot_hash` text NOT NULL,`payload_json` text NOT NULL,`frozen_at` datetime NOT NULL);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_frozen_at` ON `corporate_action_event`(`frozen_at`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_snapshot_hash` ON `corporate_action_event`(`snapshot_hash`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_source` ON `corporate_action_event`(`source`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_pay_date` ON `corporate_action_event`(`pay_date`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_record_date` ON `corporate_action_event`(`record_date`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_ex_date` ON `corporate_action_event`(`ex_date`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_coverage_end` ON `corporate_action_event`(`coverage_end`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_coverage_start` ON `corporate_action_event`(`coverage_start`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_observation_status` ON `corporate_action_event`(`observation_status`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_available_at` ON `corporate_action_event`(`available_at`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_source_at` ON `corporate_action_event`(`source_at`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_announced_at` ON `corporate_action_event`(`announced_at`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_action_type` ON `corporate_action_event`(`action_type`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_symbol` ON `corporate_action_event`(`symbol`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_version_date` ON `corporate_action_event`(`snapshot_version`,`ex_date`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_corporate_action_run_symbol_type_exdate` ON `corporate_action_event`(`run_id`,`symbol`,`action_type`,`ex_date`);
CREATE INDEX IF NOT EXISTS `idx_corporate_action_event_run_id` ON `corporate_action_event`(`run_id`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_corporate_action_run_event` ON `corporate_action_event`(`run_id`,`event_id`);
CREATE UNIQUE INDEX IF NOT EXISTS idx_strategy_order_no_trade_run ON strategy_order_event (run_id) WHERE LOWER(TRIM(event_type)) = 'no_trade';
CREATE TRIGGER IF NOT EXISTS immutable_strategy_run_snapshot_update BEFORE UPDATE ON strategy_run_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_run_snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_run_snapshot_delete BEFORE DELETE ON strategy_run_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_run_snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_candidate_snapshot_update BEFORE UPDATE ON strategy_candidate_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_candidate_snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_candidate_snapshot_delete BEFORE DELETE ON strategy_candidate_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_candidate_snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_rule_snapshot_update BEFORE UPDATE ON strategy_rule_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_rule_snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_rule_snapshot_delete BEFORE DELETE ON strategy_rule_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_rule_snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_order_event_update BEFORE UPDATE ON strategy_order_event BEGIN SELECT RAISE(ABORT, 'immutable table strategy_order_event'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_order_event_delete BEFORE DELETE ON strategy_order_event BEGIN SELECT RAISE(ABORT, 'immutable table strategy_order_event'); END;
CREATE TRIGGER IF NOT EXISTS immutable_security_master_history_update BEFORE UPDATE ON security_master_history BEGIN SELECT RAISE(ABORT, 'immutable table security_master_history'); END;
CREATE TRIGGER IF NOT EXISTS immutable_security_master_history_delete BEFORE DELETE ON security_master_history BEGIN SELECT RAISE(ABORT, 'immutable table security_master_history'); END;
CREATE TRIGGER IF NOT EXISTS immutable_corporate_action_event_update BEFORE UPDATE ON corporate_action_event BEGIN SELECT RAISE(ABORT, 'immutable table corporate_action_event'); END;
CREATE TRIGGER IF NOT EXISTS immutable_corporate_action_event_delete BEFORE DELETE ON corporate_action_event BEGIN SELECT RAISE(ABORT, 'immutable table corporate_action_event'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_backtest_run_update BEFORE UPDATE ON strategy_backtest_run BEGIN SELECT RAISE(ABORT, 'immutable table strategy_backtest_run'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_backtest_run_delete BEFORE DELETE ON strategy_backtest_run BEGIN SELECT RAISE(ABORT, 'immutable table strategy_backtest_run'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_backtest_trade_update BEFORE UPDATE ON strategy_backtest_trade BEGIN SELECT RAISE(ABORT, 'immutable table strategy_backtest_trade'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_backtest_trade_delete BEFORE DELETE ON strategy_backtest_trade BEGIN SELECT RAISE(ABORT, 'immutable table strategy_backtest_trade'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_backtest_metric_update BEFORE UPDATE ON strategy_backtest_metric BEGIN SELECT RAISE(ABORT, 'immutable table strategy_backtest_metric'); END;
CREATE TRIGGER IF NOT EXISTS immutable_strategy_backtest_metric_delete BEFORE DELETE ON strategy_backtest_metric BEGIN SELECT RAISE(ABORT, 'immutable table strategy_backtest_metric'); END;
CREATE TABLE IF NOT EXISTS `strategy_runtime_control` (`id` integer PRIMARY KEY AUTOINCREMENT,`mode` text NOT NULL,`current_strategy_version` text NOT NULL,`reason` text,`changed_by` text,`changed_at` datetime NOT NULL,`created_at` datetime,`updated_at` datetime);
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_stocks
BEFORE INSERT ON ai_recommend_stocks
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_stocks
BEFORE UPDATE ON ai_recommend_stocks
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_stocks
BEFORE DELETE ON ai_recommend_stocks
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_opening_review
BEFORE INSERT ON ai_recommend_opening_review
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_opening_review
BEFORE UPDATE ON ai_recommend_opening_review
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_opening_review
BEFORE DELETE ON ai_recommend_opening_review
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_state
BEFORE INSERT ON ai_recommend_yield_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_state
BEFORE UPDATE ON ai_recommend_yield_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_state
BEFORE DELETE ON ai_recommend_yield_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_override
BEFORE INSERT ON ai_recommend_yield_override
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_override
BEFORE UPDATE ON ai_recommend_yield_override
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_override
BEFORE DELETE ON ai_recommend_yield_override
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_record_state
BEFORE INSERT ON ai_recommend_yield_record_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_record_state
BEFORE UPDATE ON ai_recommend_yield_record_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_record_state
BEFORE DELETE ON ai_recommend_yield_record_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_meta
BEFORE INSERT ON ai_recommend_yield_meta
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_meta
BEFORE UPDATE ON ai_recommend_yield_meta
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_meta
BEFORE DELETE ON ai_recommend_yield_meta
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_dirty_code
BEFORE INSERT ON ai_recommend_yield_dirty_code
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_dirty_code
BEFORE UPDATE ON ai_recommend_yield_dirty_code
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_dirty_code
BEFORE DELETE ON ai_recommend_yield_dirty_code
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_market_summary_run_diagnostics
BEFORE INSERT ON market_summary_run_diagnostics
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_market_summary_run_diagnostics
BEFORE UPDATE ON market_summary_run_diagnostics
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_market_summary_run_diagnostics
BEFORE DELETE ON market_summary_run_diagnostics
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_run_snapshot
BEFORE INSERT ON strategy_run_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_run_snapshot
BEFORE UPDATE ON strategy_run_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_run_snapshot
BEFORE DELETE ON strategy_run_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_candidate_snapshot
BEFORE INSERT ON strategy_candidate_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_candidate_snapshot
BEFORE UPDATE ON strategy_candidate_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_candidate_snapshot
BEFORE DELETE ON strategy_candidate_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_rule_snapshot
BEFORE INSERT ON strategy_rule_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_rule_snapshot
BEFORE UPDATE ON strategy_rule_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_rule_snapshot
BEFORE DELETE ON strategy_rule_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_order_event
BEFORE INSERT ON strategy_order_event
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_order_event
BEFORE UPDATE ON strategy_order_event
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_order_event
BEFORE DELETE ON strategy_order_event
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_run_snapshot
BEFORE UPDATE ON strategy_run_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_run_snapshot
BEFORE DELETE ON strategy_run_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_candidate_snapshot
BEFORE UPDATE ON strategy_candidate_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_candidate_snapshot
BEFORE DELETE ON strategy_candidate_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_rule_snapshot
BEFORE UPDATE ON strategy_rule_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_rule_snapshot
BEFORE DELETE ON strategy_rule_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_order_event
BEFORE UPDATE ON strategy_order_event
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_order_event
BEFORE DELETE ON strategy_order_event
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END;
CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_insert
BEFORE INSERT ON ai_recommend_stocks
WHEN COALESCE(NEW.summary_version, '') <> '1.5.0'
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END;
CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_update
BEFORE UPDATE ON ai_recommend_stocks
WHEN COALESCE(OLD.summary_version, '') <> '1.5.0'
  OR COALESCE(NEW.summary_version, '') <> COALESCE(OLD.summary_version, '')
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END;
CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_delete
BEFORE DELETE ON ai_recommend_stocks
WHEN COALESCE(OLD.summary_version, '') <> '1.5.0'
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END;
