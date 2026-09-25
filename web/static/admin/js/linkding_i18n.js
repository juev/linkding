'use strict';
// English defaults for Django's admin date and time widgets.
window.gettext = (message) => message;
window.gettext_noop = (message) => message;
window.pgettext = (_context, message) => message;
window.ngettext = (singular, plural, count) => count === 1 ? singular : plural;
window.interpolate = (format, values, named) => named
    ? format.replace(/%\(\w+\)s/g, (match) => String(values[match.slice(2, -2)]))
    : format.replace(/%s/g, () => String(values.shift()));
window.get_format = (name) => ({
    DATE_INPUT_FORMATS: ['%Y-%m-%d'],
    TIME_INPUT_FORMATS: ['%H:%M:%S'],
    FIRST_DAY_OF_WEEK: 0,
})[name] ?? name;
