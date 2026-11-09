# Changelog

## Unreleased

### Added
- `validation.Struct`: validates structs by `validate` tags, nested structs and slices of structs
  included (`address.city`, `items.0.name`); field names from `json` / `form` tags.
- Rules: `required`, `required_with`, `required_without`, `accepted`, `email`, `url`, `uuid`,
  `alpha`, `alpha_num`, `alpha_dash`, `numeric`, `integer`, `min`, `max`, `size`, `between`, `gt`,
  `lt`, `oneof`, `same`, `different`, `regex`, `date`; `omitempty` / `nullable`. Rules other than
  the `required*` and `accepted` ones skip empty values, as in Laravel.
- `Errors` (`map[string][]string`) with `Add`, `First`, `Has`; Laravel-style English messages;
  `Validator.Register` for custom rules and `SetMessage` for messages per rule or per field.
- `validation.Bind(c, &in)`: reads a JSON body, a form (urlencoded or multipart) or the query string
  and validates it; invalid data is a 422 `web.HTTPError` with `{"errors": {...}}`. `DecodeForm`
  and `Invalid` for doing the steps by hand; `ErrorsOf(err)` gets the Errors back to re-render a form.
