package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.DebugLevel)
}

func main() {
	// Open database
	db, err := sql.Open("postgres", "user=robert dbname=justeat-scraping sslmode=disable")
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	// Setup colly
	c := colly.NewCollector(
		colly.Async(true),
		colly.MaxDepth(2),
	)
	c.Limit(&colly.LimitRule{DomainGlob: "*", Parallelism: 8, RandomDelay: 50 * time.Millisecond})

	c.OnRequest(func(r *colly.Request) {
		log.WithField("url", r.URL.String()).Debug("Visiting")
	})

	c.OnError(func(r *colly.Response, err error) {
		log.WithFields(log.Fields{
			"url":      r.Request.URL.String(),
			"response": r,
			"err":      err,
		}).Error("Failed")
	})

	sem := make(chan int, 500)

	c.OnResponse(func(r *colly.Response) {
		if r.Ctx.Get("type") == "restaurant_list" {
			wg.Add(1)
			sem <- 1
			go func(r *colly.Response, db *sql.DB, c *colly.Collector) {
				defer wg.Done()
				processPostcodesResponse(r, db, c)
				<-sem
			}(r, db, c)
		} else if r.Ctx.Get("type") == "restaurant_items" {
			wg.Add(1)
			sem <- 1
			go func(r *colly.Response, db *sql.DB, c *colly.Collector) {
				defer wg.Done()
				processRestaurantItems(r, db, c)
				<-sem
			}(r, db, c)
		} else if r.Ctx.Get("type") == "item_variations" {
			wg.Add(1)
			sem <- 1
			go func(r *colly.Response, db *sql.DB) {
				defer wg.Done()
				processRestaurantItemVariations(r, db)
				<-sem
			}(r, db)
		} else if r.Ctx.Get("type") == "restaurant_menu_categories" {
			wg.Add(1)
			sem <- 1
			go func(r *colly.Response, db *sql.DB, c *colly.Collector) {
				defer wg.Done()
				processRestaurantMenuCategories(r, db, c)
				<-sem
			}(r, db, c)
		} else if r.Ctx.Get("type") == "categories_to_items" {
			wg.Add(1)
			sem <- 1
			go func(r *colly.Response, db *sql.DB) {
				defer wg.Done()
				processCategoriesToItems(r, db)
				<-sem
			}(r, db)
		}
	})

	rows, err := db.Query("SELECT * FROM customers")
	if err != nil {
		panic(err)
	}

	var postcode string

	for rows.Next() {
		err = rows.Scan(&postcode)
		if err != nil {
			panic(err)
		}

		ctx := colly.NewContext()
		ctx.Put("postcode", postcode)
		ctx.Put("type", "restaurant_list")
		c.Request(
			"GET",
			fmt.Sprintf("https://uk.api.just-eat.io/restaurants/bypostcode/%s", postcode),
			nil,
			ctx,
			nil,
		)
	}

	c.Wait()

	wg.Wait()
	db.Close()
}

func processCategoriesToItems(r *colly.Response, db *sql.DB) {
	var categoriesToItemsResp CategoriesToItemsResponse
	err := json.Unmarshal(r.Body, &categoriesToItemsResp)

	if err != nil {
		panic(err)
	}

	stmt, err := db.Prepare("insert into menu_categories_to_item(item_id, menu_category_id, restaurant_id) values ($1, $2, $3) on conflict do nothing")

	if err != nil {
		panic(err)
	}

	id := r.Ctx.GetAny("restaurant_id").(int)

	for _, item := range categoriesToItemsResp.Itemids {
		_, err = stmt.Exec(item, r.Ctx.Get("category_id"), id)

		if err != nil {
			panic(err)
		}
	}

	stmt.Close()
}

func processRestaurantMenuCategories(r *colly.Response, db *sql.DB, c *colly.Collector) {
	var menuCategoriesResp RestaurantMenuCategoriesResponse
	err := json.Unmarshal(r.Body, &menuCategoriesResp)

	if err != nil {
		panic(err)
	}

	stmt, err := db.Prepare("insert into menu_categories(id, restaurant_id, name, description) values ($1, $2, $3, $4) on conflict do nothing")

	if err != nil {
		panic(err)
	}

	id := r.Ctx.GetAny("restaurant_id").(int)

	for _, category := range menuCategoriesResp.Categories {
		_, err = stmt.Exec(category.ID, id, category.Name, category.Description)

		if err != nil {
			panic(err)
		}

		ctx := colly.NewContext()
		ctx.Put("type", "categories_to_items")
		ctx.Put("restaurant_id", r.Ctx.GetAny("restaurant_id").(int))
		ctx.Put("category_id", category.ID)
		c.Request(
			"GET",
			fmt.Sprintf("https://uk.api.just-eat.io/restaurants/uk/%v/catalogue/categories/%v/items?limit=1000000", r.Ctx.GetAny("restaurant_id").(int), category.ID),
			nil,
			ctx,
			nil,
		)
	}

	stmt.Close()
}

func processRestaurantItemVariations(r *colly.Response, db *sql.DB) {
	var itemVariationsResp RestaurantItemsVariationsResponse
	err := json.Unmarshal(r.Body, &itemVariationsResp)

	if err != nil {
		panic(err)
	}

	stmt, err := db.Prepare("insert into item_variations(id, name, price, item_id) values ($1, $2, $3, $4) on conflict do nothing")

	if err != nil {
		panic(err)
	}

	for _, variation := range itemVariationsResp.Variations {
		_, err = stmt.Exec(variation.ID, variation.Name, float64(variation.Baseprice)/100, r.Ctx.Get("item_id"))

		if err != nil {
			panic(err)
		}
	}

	stmt.Close()
}

func processRestaurantItems(r *colly.Response, db *sql.DB, c *colly.Collector) {
	var itemsResp RestaurantItemsResponse
	err := json.Unmarshal(r.Body, &itemsResp)

	if err != nil {
		panic(err)
	}

	stmt, err := db.Prepare("insert into items(id, name, description, restaurant_id) values ($1, $2, $3, $4) on conflict do nothing")

	if err != nil {
		panic(err)
	}

	id := r.Ctx.GetAny("restaurant_id").(int)

	for _, item := range itemsResp.Items {
		_, err = stmt.Exec(item.ID, item.Name, item.Description, id)
		if err != nil {
			panic(err)
		}

		ctx := colly.NewContext()
		ctx.Put("type", "item_variations")
		ctx.Put("item_id", item.ID)
		c.Request(
			"GET",
			fmt.Sprintf("https://uk.api.just-eat.io/restaurants/uk/%v/catalogue/items/%v/variations?limit=1000000", r.Ctx.GetAny("restaurant_id").(int), item.ID),
			nil,
			ctx,
			nil,
		)
	}

	stmt.Close()
}

func processPostcodesResponse(r *colly.Response, db *sql.DB, c *colly.Collector) {
	var postcodeResp PostcodesResponse
	err := json.Unmarshal(r.Body, &postcodeResp)
	if err != nil {
		panic(err)
	}

	stmt1, err := db.Prepare("insert into customers_to_restaurants(customer_id, restaurant_id) values ($1, $2) on conflict do nothing")

	if err != nil {
		panic(err)
	}

	stmt2, err := db.Prepare(
		"insert into restaurants(id, name, address__first_line, address__city, address__postcode, latitude, longitude, rating__count, rating__average, description, url) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) on conflict do nothing",
	)

	if err != nil {
		panic(err)
	}

	stmt3, err := db.Prepare(
		"insert into tags(id, name) values ($1, $2) on conflict do nothing",
	)

	if err != nil {
		panic(err)
	}

	stmt4, err := db.Prepare("insert into tags_restaurants(restaurant_id, tag_id, is_top_cuisine) values ($1, $2, $3) on conflict do nothing")

	if err != nil {
		panic(err)
	}

	for _, restaurant := range postcodeResp.Restaurants {
		_, err = stmt1.Exec(r.Ctx.Get("postcode"), restaurant.ID)

		if err != nil {
			panic(err)
		}

		_, err = stmt2.Exec(
			restaurant.ID,
			restaurant.Name,
			restaurant.Address.Firstline,
			restaurant.Address.City,
			restaurant.Address.Postcode,
			restaurant.Address.Latitude,
			restaurant.Address.Longitude,
			restaurant.Rating.Count,
			restaurant.Rating.Average,
			restaurant.Description,
			restaurant.URL,
		)

		if err != nil {
			panic(err)
		}

		for _, tag := range restaurant.Cuisinetypes {
			_, err = stmt3.Exec(tag.ID, tag.Name)

			if err != nil {
				panic(err)
			}

			_, err = stmt4.Exec(restaurant.ID, tag.ID, tag.Istopcuisine)

			if err != nil {
				panic(err)
			}

			// ctx := colly.NewContext()
			// ctx.Put("type", "restaurant_items")
			// ctx.Put("restaurant_id", restaurant.ID)
			// c.Request(
			// 	"GET",
			// 	fmt.Sprintf("https://uk.api.just-eat.io/restaurants/uk/%v/catalogue/items?limit=1000000", restaurant.ID),
			// 	nil,
			// 	ctx,
			// 	nil,
			// )

			// ctx = colly.NewContext()
			// ctx.Put("type", "restaurant_menu_categories")
			// ctx.Put("restaurant_id", restaurant.ID)
			// c.Request(
			// 	"GET",
			// 	fmt.Sprintf("https://uk.api.just-eat.io/restaurants/uk/%v/catalogue/categories?limit=1000000", restaurant.ID),
			// 	nil,
			// 	ctx,
			// 	nil,
			// )
		}
	}

	stmt1.Close()
	stmt2.Close()
	stmt3.Close()
	stmt4.Close()
}

type CategoriesToItemsResponse struct {
	Itemids []string    `json:"itemIds"`
	Paging  interface{} `json:"paging"`
}

type RestaurantMenuCategoriesResponse struct {
	Categories []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"categories"`
	Paging interface{} `json:"paging"`
}

type RestaurantItemsResponse struct {
	Items []struct {
		ID                   string        `json:"id"`
		Type                 string        `json:"type"`
		Name                 string        `json:"name"`
		Requireotherproducts bool          `json:"requireOtherProducts"`
		Description          string        `json:"description"`
		Labels               []interface{} `json:"labels"`
	} `json:"items"`
	Paging interface{} `json:"paging"`
}

type RestaurantItemsVariationsResponse struct {
	Variations []struct {
		ID                string        `json:"id"`
		Name              string        `json:"name"`
		Type              string        `json:"type"`
		Baseprice         int           `json:"basePrice"`
		Dealonly          bool          `json:"dealOnly"`
		Kitchennumber     string        `json:"kitchenNumber"`
		Availabilityids   []string      `json:"availabilityIds"`
		Modifiergroupsids []string      `json:"modifierGroupsIds"`
		Dealgroupsids     []interface{} `json:"dealGroupsIds"`
	} `json:"variations"`
	Paging interface{} `json:"paging"`
}

type PostcodesResponse struct {
	Area     string `json:"Area"`
	Metadata struct {
		Canonicalname  string  `json:"CanonicalName"`
		District       string  `json:"District"`
		Postcode       string  `json:"Postcode"`
		Area           string  `json:"Area"`
		Latitude       float64 `json:"Latitude"`
		Longitude      float64 `json:"Longitude"`
		Cuisinedetails []struct {
			Name    string `json:"Name"`
			Seoname string `json:"SeoName"`
			Total   int    `json:"Total"`
		} `json:"CuisineDetails"`
		Resultcount   int         `json:"ResultCount"`
		Searchedterms interface{} `json:"SearchedTerms"`
		Tagdetails    []struct {
			Backgroundcolour string `json:"BackgroundColour"`
			Colour           string `json:"Colour"`
			Displayname      string `json:"DisplayName"`
			Key              string `json:"Key"`
			Priority         int    `json:"Priority"`
		} `json:"TagDetails"`
	} `json:"MetaData"`
	Restaurants []struct {
		ID         int    `json:"Id"`
		Name       string `json:"Name"`
		Uniquename string `json:"UniqueName"`
		Address    struct {
			City      string  `json:"City"`
			Firstline string  `json:"FirstLine"`
			Postcode  string  `json:"Postcode"`
			Latitude  float64 `json:"Latitude"`
			Longitude float64 `json:"Longitude"`
		} `json:"Address"`
		City      string  `json:"City"`
		Postcode  string  `json:"Postcode"`
		Latitude  float64 `json:"Latitude"`
		Longitude float64 `json:"Longitude"`
		Rating    struct {
			Count      int     `json:"Count"`
			Average    float64 `json:"Average"`
			Starrating float64 `json:"StarRating"`
		} `json:"Rating"`
		Ratingstars                 float64     `json:"RatingStars"`
		Numberofratings             int         `json:"NumberOfRatings"`
		Ratingaverage               float64     `json:"RatingAverage"`
		Description                 string      `json:"Description"`
		URL                         string      `json:"Url"`
		Logourl                     string      `json:"LogoUrl"`
		Istestrestaurant            bool        `json:"IsTestRestaurant"`
		Ishalal                     bool        `json:"IsHalal"`
		Isnew                       bool        `json:"IsNew"`
		Reasonwhytemporarilyoffline string      `json:"ReasonWhyTemporarilyOffline"`
		Drivedistance               float64     `json:"DriveDistance"`
		Driveinfocalculated         bool        `json:"DriveInfoCalculated"`
		Iscloseby                   bool        `json:"IsCloseBy"`
		Offerpercent                float64     `json:"OfferPercent"`
		Newnessdate                 string      `json:"NewnessDate"`
		Openingtime                 time.Time   `json:"OpeningTime"`
		Openingtimeutc              interface{} `json:"OpeningTimeUtc"`
		Openingtimeiso              string      `json:"OpeningTimeIso"`
		Openingtimelocal            string      `json:"OpeningTimeLocal"`
		Deliveryopeningtimelocal    string      `json:"DeliveryOpeningTimeLocal"`
		Deliveryopeningtime         time.Time   `json:"DeliveryOpeningTime"`
		Deliveryopeningtimeutc      interface{} `json:"DeliveryOpeningTimeUtc"`
		Deliverystarttime           time.Time   `json:"DeliveryStartTime"`
		Deliverytime                interface{} `json:"DeliveryTime"`
		Deliverytimeminutes         interface{} `json:"DeliveryTimeMinutes"`
		Deliveryworkingtimeminutes  int         `json:"DeliveryWorkingTimeMinutes"`
		Deliveryetaminutes          struct {
			Approximate interface{} `json:"Approximate"`
			Rangelower  int         `json:"RangeLower"`
			Rangeupper  int         `json:"RangeUpper"`
		} `json:"DeliveryEtaMinutes"`
		Iscollection               bool        `json:"IsCollection"`
		Isdelivery                 bool        `json:"IsDelivery"`
		Isfreedelivery             bool        `json:"IsFreeDelivery"`
		Isopennowforcollection     bool        `json:"IsOpenNowForCollection"`
		Isopennowfordelivery       bool        `json:"IsOpenNowForDelivery"`
		Isopennowforpreorder       bool        `json:"IsOpenNowForPreorder"`
		Isopennow                  bool        `json:"IsOpenNow"`
		Istemporarilyoffline       bool        `json:"IsTemporarilyOffline"`
		Deliverymenuid             int         `json:"DeliveryMenuId"`
		Collectionmenuid           interface{} `json:"CollectionMenuId"`
		Deliveryzipcode            interface{} `json:"DeliveryZipcode"`
		Deliverycost               float64     `json:"DeliveryCost"`
		Minimumdeliveryvalue       float64     `json:"MinimumDeliveryValue"`
		Seconddateranking          float64     `json:"SecondDateRanking"`
		Defaultdisplayrank         int         `json:"DefaultDisplayRank"`
		Sponsoredposition          int         `json:"SponsoredPosition"`
		Seconddaterank             float64     `json:"SecondDateRank"`
		Score                      float64     `json:"Score"`
		Istemporaryboost           bool        `json:"IsTemporaryBoost"`
		Issponsored                bool        `json:"IsSponsored"`
		Ispremier                  bool        `json:"IsPremier"`
		Hygienerating              interface{} `json:"HygieneRating"`
		Showsmiley                 bool        `json:"ShowSmiley"`
		Smileydate                 interface{} `json:"SmileyDate"`
		Smileyelite                bool        `json:"SmileyElite"`
		Smileyresult               interface{} `json:"SmileyResult"`
		Smileyurl                  interface{} `json:"SmileyUrl"`
		Sendsonitswaynotifications bool        `json:"SendsOnItsWayNotifications"`
		Brandname                  string      `json:"BrandName"`
		Isbrand                    bool        `json:"IsBrand"`
		Lastupdated                string      `json:"LastUpdated"`
		Deals                      []struct {
			Description     string  `json:"Description"`
			Discountpercent float64 `json:"DiscountPercent"`
			Qualifyingprice float64 `json:"QualifyingPrice"`
			Offertype       string  `json:"OfferType"`
		} `json:"Deals"`
		Offers []struct {
			Amount             float64 `json:"Amount"`
			Qualifyingvalue    float64 `json:"QualifyingValue"`
			Maxqualifyingvalue float64 `json:"MaxQualifyingValue"`
			Type               string  `json:"Type"`
			Offerid            string  `json:"OfferId"`
		} `json:"Offers"`
		Logo []struct {
			Standardresolutionurl string `json:"StandardResolutionURL"`
		} `json:"Logo"`
		Tags                []interface{} `json:"Tags"`
		Deliverychargebands []interface{} `json:"DeliveryChargeBands"`
		Cuisinetypes        []struct {
			ID           int    `json:"Id"`
			Istopcuisine bool   `json:"IsTopCuisine"`
			Name         string `json:"Name"`
			Seoname      string `json:"SeoName"`
		} `json:"CuisineTypes"`
		Cuisines []struct {
			Name    string `json:"Name"`
			Seoname string `json:"SeoName"`
		} `json:"Cuisines"`
		Scoremetadata []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"ScoreMetaData"`
		Badges           []interface{} `json:"Badges"`
		Openingtimes     []interface{} `json:"OpeningTimes"`
		Serviceableareas []interface{} `json:"ServiceableAreas"`
	} `json:"Restaurants"`
	Restaurantsets []interface{} `json:"RestaurantSets"`
	Cuisinesets    []struct {
		ID       string `json:"Id"`
		Name     string `json:"Name"`
		Type     string `json:"Type"`
		Cuisines []struct {
			Name    string `json:"Name"`
			Seoname string `json:"SeoName"`
		} `json:"Cuisines"`
	} `json:"CuisineSets"`
	Views             []interface{} `json:"Views"`
	Dishes            []interface{} `json:"Dishes"`
	Shortresulttext   string        `json:"ShortResultText"`
	Deliveryfees      interface{}   `json:"deliveryFees"`
	Promotedplacement interface{}   `json:"promotedPlacement"`
}
