-- Identifiers move from the dashed 8-4-4-4-12 form to the canonical wire
-- form: 32 lowercase hex digits with no dashes (wire.id.R1). Every id column
-- (and the id lists stored inside the user policy and configuration
-- documents) is rewritten in place; values of any other shape (client
-- chosen device ids, image tags that are not GUIDs) are left alone.
--
-- Foreign keys are checked at COMMIT, after every referencing column has
-- been rewritten alongside the key it points at.
PRAGMA defer_foreign_keys = ON;

UPDATE libraries SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END;

UPDATE items SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END,
    library_id = CASE WHEN length(library_id) = 36 THEN lower(replace(library_id, '-', '')) ELSE library_id END,
    parent_id = CASE WHEN length(parent_id) = 36 THEN lower(replace(parent_id, '-', '')) ELSE parent_id END,
    series_id = CASE WHEN length(series_id) = 36 THEN lower(replace(series_id, '-', '')) ELSE series_id END,
    season_id = CASE WHEN length(season_id) = 36 THEN lower(replace(season_id, '-', '')) ELSE season_id END;

UPDATE item_ancestors SET
    item_id = CASE WHEN length(item_id) = 36 THEN lower(replace(item_id, '-', '')) ELSE item_id END,
    ancestor_id = CASE WHEN length(ancestor_id) = 36 THEN lower(replace(ancestor_id, '-', '')) ELSE ancestor_id END;

UPDATE item_relations SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END,
    parent_id = CASE WHEN length(parent_id) = 36 THEN lower(replace(parent_id, '-', '')) ELSE parent_id END,
    child_id = CASE WHEN length(child_id) = 36 THEN lower(replace(child_id, '-', '')) ELSE child_id END;

UPDATE users SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END;

UPDATE auth_tokens SET
    user_id = CASE WHEN length(user_id) = 36 THEN lower(replace(user_id, '-', '')) ELSE user_id END;

UPDATE display_preferences SET
    user_id = CASE WHEN length(user_id) = 36 THEN lower(replace(user_id, '-', '')) ELSE user_id END;

UPDATE devices SET
    last_user_id = CASE WHEN length(last_user_id) = 36 THEN lower(replace(last_user_id, '-', '')) ELSE last_user_id END;

UPDATE playstates SET
    user_id = CASE WHEN length(user_id) = 36 THEN lower(replace(user_id, '-', '')) ELSE user_id END,
    item_id = CASE WHEN length(item_id) = 36 THEN lower(replace(item_id, '-', '')) ELSE item_id END;

UPDATE image_refs SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END,
    item_id = CASE WHEN length(item_id) = 36 THEN lower(replace(item_id, '-', '')) ELSE item_id END,
    tag = CASE WHEN length(tag) = 36 THEN lower(replace(tag, '-', '')) ELSE tag END;

UPDATE trickplay_info SET
    item_id = CASE WHEN length(item_id) = 36 THEN lower(replace(item_id, '-', '')) ELSE item_id END;

UPDATE subtitle_refs SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END,
    item_id = CASE WHEN length(item_id) = 36 THEN lower(replace(item_id, '-', '')) ELSE item_id END;

UPDATE lyric_refs SET
    id = CASE WHEN length(id) = 36 THEN lower(replace(id, '-', '')) ELSE id END,
    item_id = CASE WHEN length(item_id) = 36 THEN lower(replace(item_id, '-', '')) ELSE item_id END;

UPDATE activity_log SET
    user_id = CASE WHEN length(user_id) = 36 THEN lower(replace(user_id, '-', '')) ELSE user_id END;

UPDATE user_configuration SET
    user_id = CASE WHEN length(user_id) = 36 THEN lower(replace(user_id, '-', '')) ELSE user_id END;

-- Id lists inside the stored documents.

UPDATE users SET policy_json = json_set(policy_json, '$.EnabledFolders', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(policy_json, '$.EnabledFolders'))))
WHERE json_valid(policy_json) AND json_type(policy_json, '$.EnabledFolders') = 'array';

UPDATE users SET policy_json = json_set(policy_json, '$.EnabledChannels', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(policy_json, '$.EnabledChannels'))))
WHERE json_valid(policy_json) AND json_type(policy_json, '$.EnabledChannels') = 'array';

UPDATE users SET policy_json = json_set(policy_json, '$.BlockedMediaFolders', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(policy_json, '$.BlockedMediaFolders'))))
WHERE json_valid(policy_json) AND json_type(policy_json, '$.BlockedMediaFolders') = 'array';

UPDATE users SET policy_json = json_set(policy_json, '$.BlockedChannels', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(policy_json, '$.BlockedChannels'))))
WHERE json_valid(policy_json) AND json_type(policy_json, '$.BlockedChannels') = 'array';

UPDATE users SET policy_json = json_set(policy_json, '$.EnableContentDeletionFromFolders', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(policy_json, '$.EnableContentDeletionFromFolders'))))
WHERE json_valid(policy_json) AND json_type(policy_json, '$.EnableContentDeletionFromFolders') = 'array';

UPDATE user_configuration SET json = json_set(json, '$.GroupedFolders', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(user_configuration.json, '$.GroupedFolders'))))
WHERE json_valid(json) AND json_type(json, '$.GroupedFolders') = 'array';

UPDATE user_configuration SET json = json_set(json, '$.OrderedViews', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(user_configuration.json, '$.OrderedViews'))))
WHERE json_valid(json) AND json_type(json, '$.OrderedViews') = 'array';

UPDATE user_configuration SET json = json_set(json, '$.LatestItemsExcludes', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(user_configuration.json, '$.LatestItemsExcludes'))))
WHERE json_valid(json) AND json_type(json, '$.LatestItemsExcludes') = 'array';

UPDATE user_configuration SET json = json_set(json, '$.MyMediaExcludes', json((
    SELECT json_group_array(CASE WHEN length(value) = 36 THEN lower(replace(value, '-', '')) ELSE value END) FROM json_each(user_configuration.json, '$.MyMediaExcludes'))))
WHERE json_valid(json) AND json_type(json, '$.MyMediaExcludes') = 'array';
