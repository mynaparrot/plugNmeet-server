CREATE DATABASE IF NOT EXISTS `plugnmeet` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE `plugnmeet`;

CREATE TABLE IF NOT EXISTS `pnm_room_info` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `room_title` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  `roomId` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL,
  `sid` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL,
  `joined_participants` int(10) NOT NULL DEFAULT 0,
  `is_running` int(1) NOT NULL,
  `is_recording` int(1) NOT NULL DEFAULT 0,
  `recorder_id` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  `is_active_rtmp` int(1) NOT NULL DEFAULT 0,
  `rtmp_node_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  `webhook_url` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  `creation_time` int(10) NOT NULL,
  `created` datetime NOT NULL DEFAULT current_timestamp(),
  `ended` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `modified` datetime NOT NULL DEFAULT '0000-00-00 00:00:00' ON UPDATE current_timestamp(),
  PRIMARY KEY (`id`),
  UNIQUE KEY `sid` (`sid`),
  KEY `roomId` (`roomId`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `pnm_recordings` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `record_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL,
  `room_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL,
  `room_sid` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL,
  `recorder_id` varchar(36) COLLATE utf8mb4_unicode_ci NOT NULL,
  `file_path` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL,
  `size` double NOT NULL,
  `published` int(1) NOT NULL DEFAULT 1,
  `creation_time` int(10) NOT NULL DEFAULT 0,
  `room_creation_time` int(10) NOT NULL DEFAULT 0,
  `created` datetime NOT NULL DEFAULT current_timestamp(),
  `modified` datetime NOT NULL DEFAULT '0000-00-00 00:00:00' ON UPDATE current_timestamp(),
  PRIMARY KEY (`id`),
  UNIQUE KEY `record_id` (`record_id`),
  KEY `room_id` (`room_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


CREATE TABLE IF NOT EXISTS `pnm_recorder` (
    `id` int(11) NOT NULL AUTO_INCREMENT,
    `recorder_id` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL,
    `max_limit` int(10) NOT NULL,
    `current_progress` int(10) NOT NULL DEFAULT 0,
    `last_ping` int(10) NOT NULL DEFAULT 0,
    `created` datetime NOT NULL DEFAULT current_timestamp(),
    `modified` datetime NOT NULL DEFAULT '0000-00-00 00:00:00' ON UPDATE current_timestamp(),
    PRIMARY KEY (`id`),
    UNIQUE KEY `recorder_id` (`recorder_id`)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
