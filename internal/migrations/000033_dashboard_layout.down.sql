ALTER TABLE member_permissions DROP CONSTRAINT member_permissions_module_check;
ALTER TABLE member_permissions ADD CONSTRAINT member_permissions_module_check
    CHECK (module IN ('categories', 'clients', 'loans', 'expenses', 'income', 'assets',
        'documents', 'people', 'personinfo', 'health', 'pets', 'reports', 'notifications',
        'integrations', 'finance'));

DROP TABLE IF EXISTS dashboard_layouts;
