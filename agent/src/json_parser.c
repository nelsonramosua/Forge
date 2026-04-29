#define _POSIX_C_SOURCE 200809L

#include "json_parser.h"

#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct json_object_entry {
    char *key;
    json_value_t *value;
} json_object_entry_t;

struct json_value {
    json_type_t type;
    union {
        bool boolean;
        double number;
        char *string;
        struct {
            json_value_t **items;
            size_t count;
            size_t capacity;
        } array;
        struct {
            json_object_entry_t *entries;
            size_t count;
            size_t capacity;
        } object;
    } data;
};

typedef struct parser {
    const char *cur;
    const char *start;
    char *error;
} parser_t;

static void skip_ws(parser_t *parser) {
    while (*parser->cur && isspace((unsigned char)*parser->cur)) {
        parser->cur++;
    }
}

static json_value_t *parse_value(parser_t *parser);

static json_value_t *new_value(json_type_t type) {
    json_value_t *value = calloc(1, sizeof(*value));
    if (!value) {
        return NULL;
    }
    value->type = type;
    return value;
}

static bool append_byte(char **buffer, size_t *len, size_t *cap, unsigned char byte) {
    if (*len + 1 >= *cap) {
        size_t next = *cap ? *cap * 2 : 32;
        char *resized = realloc(*buffer, next);
        if (!resized) {
            return false;
        }
        *buffer = resized;
        *cap = next;
    }
    (*buffer)[(*len)++] = (char)byte;
    return true;
}

static bool append_utf8(char **buffer, size_t *len, size_t *cap, unsigned int codepoint) {
    if (codepoint <= 0x7F) {
        return append_byte(buffer, len, cap, (unsigned char)codepoint);
    }
    if (codepoint <= 0x7FF) {
        return append_byte(buffer, len, cap, (unsigned char)(0xC0 | (codepoint >> 6))) &&
               append_byte(buffer, len, cap, (unsigned char)(0x80 | (codepoint & 0x3F)));
    }
    if (codepoint >= 0xD800 && codepoint <= 0xDFFF) {
        return false;
    }
    if (codepoint <= 0xFFFF) {
        return append_byte(buffer, len, cap, (unsigned char)(0xE0 | (codepoint >> 12))) &&
               append_byte(buffer, len, cap, (unsigned char)(0x80 | ((codepoint >> 6) & 0x3F))) &&
               append_byte(buffer, len, cap, (unsigned char)(0x80 | (codepoint & 0x3F)));
    }
    if (codepoint <= 0x10FFFF) {
        return append_byte(buffer, len, cap, (unsigned char)(0xF0 | (codepoint >> 18))) &&
               append_byte(buffer, len, cap, (unsigned char)(0x80 | ((codepoint >> 12) & 0x3F))) &&
               append_byte(buffer, len, cap, (unsigned char)(0x80 | ((codepoint >> 6) & 0x3F))) &&
               append_byte(buffer, len, cap, (unsigned char)(0x80 | (codepoint & 0x3F)));
    }
    return false;
}

static int hex_value(char c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return c - 'a' + 10;
    }
    if (c >= 'A' && c <= 'F') {
        return c - 'A' + 10;
    }
    return -1;
}

static bool parse_unicode_escape(parser_t *parser, unsigned int *codepoint) {
    unsigned int cp = 0;
    for (int i = 0; i < 4; i++) {
        int value = hex_value(parser->cur[i]);
        if (value < 0) {
            return false;
        }
        cp = (cp << 4) | (unsigned int)value;
    }
    parser->cur += 4;
    *codepoint = cp;
    return true;
}

static bool append_escape(parser_t *parser, char **buffer, size_t *len, size_t *cap) {
    char esc = *parser->cur++;
    switch (esc) {
        case '"':
        case '\\':
        case '/':
            return append_byte(buffer, len, cap, (unsigned char)esc);
        case 'b':
            return append_byte(buffer, len, cap, '\b');
        case 'f':
            return append_byte(buffer, len, cap, '\f');
        case 'n':
            return append_byte(buffer, len, cap, '\n');
        case 'r':
            return append_byte(buffer, len, cap, '\r');
        case 't':
            return append_byte(buffer, len, cap, '\t');
        case 'u': {
            unsigned int cp = 0;
            if (!parse_unicode_escape(parser, &cp)) {
                return false;
            }
            if (cp >= 0xD800 && cp <= 0xDBFF && parser->cur[0] == '\\' && parser->cur[1] == 'u') {
                parser->cur += 2;
                unsigned int low = 0;
                if (!parse_unicode_escape(parser, &low) || low < 0xDC00 || low > 0xDFFF) {
                    return false;
                }
                cp = 0x10000 + (((cp - 0xD800) << 10) | (low - 0xDC00));
            }
            return append_utf8(buffer, len, cap, cp);
        }
        default:
            return false;
    }
}

static char *parse_string_raw(parser_t *parser) {
    if (*parser->cur != '"') {
        return NULL;
    }
    parser->cur++;
    char *buffer = NULL;
    size_t len = 0;
    size_t cap = 0;
    while (*parser->cur && *parser->cur != '"') {
        unsigned char c = (unsigned char)*parser->cur++;
        if (c == '\\') {
            if (*parser->cur == '\0' || !append_escape(parser, &buffer, &len, &cap)) {
                free(buffer);
                return NULL;
            }
            continue;
        }
        if (c < 0x20) {
            free(buffer);
            return NULL;
        }
        if (!append_byte(&buffer, &len, &cap, c)) {
            free(buffer);
            return NULL;
        }
    }
    if (*parser->cur != '"') {
        free(buffer);
        return NULL;
    }
    parser->cur++;
    if (!append_byte(&buffer, &len, &cap, '\0')) {
        free(buffer);
        return NULL;
    }
    return buffer;
}

static json_value_t *parse_string(parser_t *parser) {
    char *text = parse_string_raw(parser);
    if (!text) {
        return NULL;
    }
    json_value_t *value = new_value(JSON_STRING);
    if (!value) {
        free(text);
        return NULL;
    }
    value->data.string = text;
    return value;
}

static json_value_t *parse_number(parser_t *parser) {
    errno = 0;
    char *end = NULL;
    double number = strtod(parser->cur, &end);
    if (end == parser->cur || errno == ERANGE) {
        return NULL;
    }
    parser->cur = end;
    json_value_t *value = new_value(JSON_NUMBER);
    if (!value) {
        return NULL;
    }
    value->data.number = number;
    return value;
}

static json_value_t *parse_literal(parser_t *parser, const char *literal, json_type_t type, bool boolean_value) {
    size_t len = strlen(literal);
    if (strncmp(parser->cur, literal, len) != 0) {
        return NULL;
    }
    parser->cur += len;
    json_value_t *value = new_value(type);
    if (!value) {
        return NULL;
    }
    if (type == JSON_BOOL) {
        value->data.boolean = boolean_value;
    }
    return value;
}

static bool append_array_item(json_value_t *array, json_value_t *item) {
    if (array->data.array.count == array->data.array.capacity) {
        size_t next = array->data.array.capacity ? array->data.array.capacity * 2 : 4;
        json_value_t **items = realloc(array->data.array.items, next * sizeof(*items));
        if (!items) {
            return false;
        }
        array->data.array.items = items;
        array->data.array.capacity = next;
    }
    array->data.array.items[array->data.array.count++] = item;
    return true;
}

static bool append_object_entry(json_value_t *object, char *key, json_value_t *value) {
    if (object->data.object.count == object->data.object.capacity) {
        size_t next = object->data.object.capacity ? object->data.object.capacity * 2 : 4;
        json_object_entry_t *entries = realloc(object->data.object.entries, next * sizeof(*entries));
        if (!entries) {
            return false;
        }
        object->data.object.entries = entries;
        object->data.object.capacity = next;
    }
    object->data.object.entries[object->data.object.count++] = (json_object_entry_t){.key = key, .value = value};
    return true;
}

static json_value_t *parse_array(parser_t *parser) {
    if (*parser->cur != '[') {
        return NULL;
    }
    parser->cur++;
    json_value_t *array = new_value(JSON_ARRAY);
    if (!array) {
        return NULL;
    }
    skip_ws(parser);
    if (*parser->cur == ']') {
        parser->cur++;
        return array;
    }
    while (*parser->cur) {
        skip_ws(parser);
        json_value_t *item = parse_value(parser);
        if (!item || !append_array_item(array, item)) {
            json_value_free(item);
            json_value_free(array);
            return NULL;
        }
        skip_ws(parser);
        if (*parser->cur == ']') {
            parser->cur++;
            return array;
        }
        if (*parser->cur != ',') {
            json_value_free(array);
            return NULL;
        }
        parser->cur++;
    }
    json_value_free(array);
    return NULL;
}

static json_value_t *parse_object(parser_t *parser) {
    if (*parser->cur != '{') {
        return NULL;
    }
    parser->cur++;
    json_value_t *object = new_value(JSON_OBJECT);
    if (!object) {
        return NULL;
    }
    skip_ws(parser);
    if (*parser->cur == '}') {
        parser->cur++;
        return object;
    }
    while (*parser->cur) {
        skip_ws(parser);
        char *key = parse_string_raw(parser);
        if (!key) {
            json_value_free(object);
            return NULL;
        }
        skip_ws(parser);
        if (*parser->cur != ':') {
            free(key);
            json_value_free(object);
            return NULL;
        }
        parser->cur++;
        skip_ws(parser);
        json_value_t *item = parse_value(parser);
        if (!item || !append_object_entry(object, key, item)) {
            free(key);
            json_value_free(item);
            json_value_free(object);
            return NULL;
        }
        skip_ws(parser);
        if (*parser->cur == '}') {
            parser->cur++;
            return object;
        }
        if (*parser->cur != ',') {
            json_value_free(object);
            return NULL;
        }
        parser->cur++;
    }
    json_value_free(object);
    return NULL;
}

static json_value_t *parse_value(parser_t *parser) {
    skip_ws(parser);
    switch (*parser->cur) {
        case '"':
            return parse_string(parser);
        case '{':
            return parse_object(parser);
        case '[':
            return parse_array(parser);
        case 't':
            return parse_literal(parser, "true", JSON_BOOL, true);
        case 'f':
            return parse_literal(parser, "false", JSON_BOOL, false);
        case 'n':
            return parse_literal(parser, "null", JSON_NULL, false);
        default:
            if (*parser->cur == '-' || isdigit((unsigned char)*parser->cur)) {
                return parse_number(parser);
            }
            return NULL;
    }
}

json_value_t *json_parse(const char *input, char **error_message) {
    parser_t parser = {.cur = input, .start = input, .error = NULL};
    json_value_t *value = parse_value(&parser);
    skip_ws(&parser);
    if (!value || *parser.cur != '\0') {
        json_value_free(value);
        if (error_message) {
            *error_message = parser.error ? parser.error : strdup("invalid JSON");
        } else {
            free(parser.error);
        }
        return NULL;
    }
    if (error_message) {
        *error_message = NULL;
    }
    free(parser.error);
    return value;
}

void json_value_free(json_value_t *value) {
    if (!value) {
        return;
    }
    switch (value->type) {
        case JSON_STRING:
            free(value->data.string);
            break;
        case JSON_ARRAY:
            for (size_t i = 0; i < value->data.array.count; i++) {
                json_value_free(value->data.array.items[i]);
            }
            free(value->data.array.items);
            break;
        case JSON_OBJECT:
            for (size_t i = 0; i < value->data.object.count; i++) {
                free(value->data.object.entries[i].key);
                json_value_free(value->data.object.entries[i].value);
            }
            free(value->data.object.entries);
            break;
        default:
            break;
    }
    free(value);
}

const json_value_t *json_object_get(const json_value_t *object, const char *key) {
    if (!object || object->type != JSON_OBJECT) {
        return NULL;
    }
    for (size_t i = 0; i < object->data.object.count; i++) {
        if (strcmp(object->data.object.entries[i].key, key) == 0) {
            return object->data.object.entries[i].value;
        }
    }
    return NULL;
}

size_t json_object_size(const json_value_t *object) {
    if (!object || object->type != JSON_OBJECT) {
        return 0;
    }
    return object->data.object.count;
}

const char *json_object_key_at(const json_value_t *object, size_t index) {
    if (!object || object->type != JSON_OBJECT || index >= object->data.object.count) {
        return NULL;
    }
    return object->data.object.entries[index].key;
}

const json_value_t *json_object_value_at(const json_value_t *object, size_t index) {
    if (!object || object->type != JSON_OBJECT || index >= object->data.object.count) {
        return NULL;
    }
    return object->data.object.entries[index].value;
}

size_t json_array_size(const json_value_t *array) {
    if (!array || array->type != JSON_ARRAY) {
        return 0;
    }
    return array->data.array.count;
}

const json_value_t *json_array_get(const json_value_t *array, size_t index) {
    if (!array || array->type != JSON_ARRAY || index >= array->data.array.count) {
        return NULL;
    }
    return array->data.array.items[index];
}

bool json_value_as_string(const json_value_t *value, char *out, size_t out_len) {
    if (!value || value->type != JSON_STRING || !out || out_len == 0) {
        return false;
    }
    size_t len = strlen(value->data.string);
    if (len + 1 > out_len) {
        return false;
    }
    memcpy(out, value->data.string, len + 1);
    return true;
}

char *json_value_strdup(const json_value_t *value) {
    if (!value || value->type != JSON_STRING) {
        return NULL;
    }
    return strdup(value->data.string);
}

bool json_value_as_long(const json_value_t *value, long *out) {
    if (!value || !out) {
        return false;
    }
    if (value->type == JSON_NUMBER) {
        if (value->data.number < (double)LONG_MIN || value->data.number > (double)LONG_MAX) {
            return false;
        }
        *out = (long)value->data.number;
        return true;
    }
    if (value->type == JSON_BOOL) {
        *out = value->data.boolean ? 1 : 0;
        return true;
    }
    return false;
}

bool json_value_as_double(const json_value_t *value, double *out) {
    if (!value || !out) {
        return false;
    }
    if (value->type != JSON_NUMBER) {
        return false;
    }
    *out = value->data.number;
    return true;
}