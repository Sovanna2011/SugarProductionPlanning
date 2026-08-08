sap.ui.define([
	"sap/ui/core/UIComponent",
	"sap/ui/model/json/JSONModel",
	"sap/ui/Device"
], function (UIComponent, JSONModel, Device) {
	"use strict";

	return UIComponent.extend("kss.spp.Component", {

		metadata: {
			manifest: "json"
		},

		init: function () {
			UIComponent.prototype.init.apply(this, arguments);

			this.setModel(new JSONModel(Device), "device");

			// The dashboard state: filters, the loaded payload and busy flags.
			this.setModel(new JSONModel({
				busy: false,
				loaded: false,
				error: "",
				filters: {
					factoryId: 0,
					date: null,
					storageType: "",
					storageCode: "",
					productCode: "",
					packagingCode: ""
				},
				factories: [],
				storageTypes: [],
				storageLocations: [],
				products: [],
				packagingTypes: [],
				dashboard: {
					groups: [],
					storages: [],
					alerts: []
				},
				projection: null,
				projectionScopes: []
			}), "app");
		},

		/**
		 * Base URL of the planning API. Same origin by default, which is how the
		 * Go server serves this app; override with ?api=http://host:port for a
		 * split deployment.
		 */
		getApiBase: function () {
			var sOverride = new URLSearchParams(window.location.search).get("api");
			return sOverride ? sOverride.replace(/\/$/, "") : "";
		}
	});
});
