CREATE TABLE workspace_business_projects (
    id NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
    name NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY NONCLUSTERED (id)
);
GO
CREATE TABLE workspace_products (
    id NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
    business_project_id NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
    name NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY NONCLUSTERED (id),
    FOREIGN KEY (business_project_id) REFERENCES workspace_business_projects (id) ON DELETE NO ACTION
);
GO
CREATE TABLE workspace_applications (
    application_id NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
    product_id NVARCHAR(64) COLLATE Latin1_General_100_BIN2,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY NONCLUSTERED (application_id),
    FOREIGN KEY (application_id) REFERENCES projects (id) ON DELETE NO ACTION,
    FOREIGN KEY (product_id) REFERENCES workspace_products (id) ON DELETE NO ACTION
);
GO
CREATE INDEX workspace_products_parent ON workspace_products (business_project_id);
GO
CREATE INDEX workspace_applications_product ON workspace_applications (product_id);
