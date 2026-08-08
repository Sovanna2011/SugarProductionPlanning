sap.ui.define([
	"kss/spp/controller/BaseController"
], function (BaseController) {
	"use strict";

	return BaseController.extend("kss.spp.controller.App", {
		onInit: function () {
			// Nothing to do beyond letting the router take over; the shell
			// exists only to host the routed pages.
		}
	});
});
