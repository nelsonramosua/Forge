#ifndef FORGE_JSON_PARSER_H
#define FORGE_JSON_PARSER_H

#include <stdbool.h>
#include <stddef.h>

typedef enum {
    JSON_NULL,
    JSON_BOOL,
    JSON_NUMBER,
    JSON_STRING,
    JSON_ARRAY,
    JSON_OBJECT,
} json_type_t;

typedef struct json_value json_value_t;

json_value_t *json_parse(const char *input, char **error_message);
void json_value_free(json_value_t *value);

const json_value_t *json_object_get(const json_value_t *object, const char *key);
size_t json_object_size(const json_value_t *object);
const char *json_object_key_at(const json_value_t *object, size_t index);
const json_value_t *json_object_value_at(const json_value_t *object, size_t index);

size_t json_array_size(const json_value_t *array);
const json_value_t *json_array_get(const json_value_t *array, size_t index);

bool json_value_as_string(const json_value_t *value, char *out, size_t out_len);
char *json_value_strdup(const json_value_t *value);
bool json_value_as_long(const json_value_t *value, long *out);
bool json_value_as_double(const json_value_t *value, double *out);

#endif