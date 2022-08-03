create table customers_to_restaurants(
    customer_id varchar(8),
    restaurant_id int,
    primary key (customer_id, restaurant_id)
);

create table restaurants(
    id int primary key,
    name text,
    address__first_line text,
    address__city text,
    address__postcode text,
    latitude real,
    longitude real,
    rating__count int,
    rating__average real,
    description text,
    url text
);

create table tags(
    id int primary key,
    name text
);

create table tags_restaurants(
    restaurant_id int,
    tag_id int,
    is_top_cuisine boolean,
    primary key(restaurant_id, tag_id)
);

-- create table menu_categories(
--     id text,
--     restaurant_id int,
--     name text,
--     description text,
--     primary key (id, restaurant_id)
-- );

-- create table items(
--     id text primary key,
--     name text,
--     description text,
--     restaurant_id int
-- );

-- create table menu_categories_to_item(
--     item_id text,
--     menu_category_id text,
--     restaurant_id int,
--     primary key (item_id, menu_category_id, restaurant_id)
-- );

-- create table item_variations(
--     id text primary key,
--     name text,
--     price money,
--     item_id text
-- );